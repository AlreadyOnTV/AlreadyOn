#!/usr/bin/env python3
"""Publish signed Android releases with fresh APKs for each release stage."""
import argparse
import sys
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time
sys.path.insert(0, str(Path(__file__).parents[1] / 'security'))
from public_inventory import NAMES as SBOM_NAMES, verify_bundle
SBOM_ASSETS = (*SBOM_NAMES, 'sbom-manifest.json')

from image_registry import release_source as image_source
from release_dates import release_day
from release_notes import candidate_changes, promotion_changes, release_body

# Current lifecycle is Nightly on main plus manual Stable. Historical tags remain readable.
NIGHTLY = re.compile(r"(\d{2}\.\d{2}\.\d{2})-nightly\Z")
ALPHA = re.compile(r"((?:\d{2}\.\d{2}\.\d{2}|(?:20)?\d{2}-\d{2}-\d{2}))-alpha([1-9]\d*)\Z")
RC = re.compile(r"((?:\d{2}\.\d{2}\.\d{2}|(?:20)?\d{2}-\d{2}-\d{2}))-[Rr][Cc]([1-9]\d*)\Z")
STABLE = re.compile(r"((?:\d{2}\.\d{2}\.\d{2}|(?:20)?\d{2}-\d{2}-\d{2}))-(?:Stable|stable)\Z")
LEGACY = re.compile(r"((?:\d{2}\.\d{2}\.\d{2}|(?:20)?\d{2}-\d{2}-\d{2}))-(beta|rc|stable)([1-9]\d*)?\Z")
CANDIDATE = re.compile(r"((?:\d{2}\.\d{2}\.\d{2}|(?:20)?\d{2}-\d{2}-\d{2}))-(?:alpha[1-9]\d*|RC[1-9]\d*|beta(?:[1-9]\d*)?|rc[1-9]\d*)\Z")
ASSETS = ("alreadyon-android.apk", "SHA256SUMS", "android-release.json", "alreadyon-tv.apk")


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def api(endpoint):
    return json.loads(run("gh", "api", endpoint))


def releases(repo):
    pages = json.loads(run("gh", "api", "--paginate", "--slurp",
                           f"repos/{repo}/releases?per_page=100"))
    return [release for page in pages for release in page]


def stage_tag(tag):
    if match := NIGHTLY.fullmatch(tag):
        return release_day(match[1]), "nightly", 0
    if match := ALPHA.fullmatch(tag):
        return release_day(match[1], historical=True), "alpha", int(match[2])
    if match := RC.fullmatch(tag):
        return release_day(match[1], historical=True), "rc", int(match[2])
    if match := STABLE.fullmatch(tag):
        return release_day(match[1], historical=True), "stable", 0
    if match := LEGACY.fullmatch(tag):
        return release_day(match[1], historical=True), match[2], int(match[3] or 0)
    return None


def previous_release_tag(entries, day):
    """Choose the latest published Android release from an earlier date."""
    candidates = []
    for entry in entries:
        parsed = stage_tag(entry["tag_name"])
        if entry.get("draft", False) or parsed is None or parsed[0] >= day:
            continue
        rank = {"nightly": -1, "alpha": 0, "beta": 1, "rc": 2, "stable": 3}[parsed[1]]
        candidates.append((parsed[0], rank, parsed[2], entry["tag_name"]))
    return max(candidates)[3] if candidates else None


def next_version_code(entries):
    codes = [tag_version_code(entry) for entry in entries
             if re.search(r"Android versionCode: `(\d+)`", entry.get("body") or "")]
    code = max(int(time.time()) - 1577836800, max(codes, default=0) + 1)
    if not 0 < code <= 2100000000:
        raise ValueError("Android version code exhausted")
    return code


def tag_version_code(entry):
    match = re.search(r"Android versionCode: `(\d+)`", entry.get("body") or "")
    if match is None:
        raise ValueError("Incomplete Android draft has no version code")
    return int(match[1])


def changelog_baseline(entries, tag):
    """Use the preceding release stage for this date, else the latest prior date."""
    current = stage_tag(tag)
    if current is None:
        raise ValueError("Invalid release stage tag")
    same_day = []
    for entry in entries:
        previous = stage_tag(entry["tag_name"])
        if entry.get("draft", False) or previous is None or previous[0] != current[0]:
            continue
        before = (previous[1] == "nightly" and current[1] != "nightly"
                  or previous[1] == "alpha" and current[1] == "alpha" and previous[2] < current[2]
                  or previous[1] == "alpha" and current[1] == "rc"
                  or previous[1] == "rc" and current[1] == "rc" and previous[2] < current[2]
                  or current[1] == "stable" and previous[1] in {"alpha", "rc"})
        if before:
            rank = {"nightly": -1, "alpha": 0, "rc": 1}[previous[1]]
            same_day.append((rank, previous[2], entry["tag_name"]))
    return max(same_day)[2] if same_day else previous_release_tag(entries, current[0])


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def release_assets(directory):
    present = {name for name in SBOM_ASSETS if (directory / name).exists()}
    if present and present != set(SBOM_ASSETS):
        raise ValueError('Incomplete release SBOM assets')
    return (*ASSETS, *SBOM_ASSETS) if present else ASSETS


def verify_assets(directory, source=None):
    manifest = json.loads((directory / "android-release.json").read_text())
    if manifest.get("package") != "io.github.alreadyonapp.alreadyon":
        raise ValueError("Unexpected Android application identity")
    if source is not None and manifest.get("source_sha") != source:
        raise ValueError("Candidate source revision does not match its tag")
    if not (CANDIDATE.fullmatch(manifest.get("version_name", ""))
            or STABLE.fullmatch(manifest.get("version_name", ""))
            or NIGHTLY.fullmatch(manifest.get("version_name", ""))):
        raise ValueError("Invalid candidate version")
    expected = digest(directory / ASSETS[0])
    if manifest.get("apk_sha256") != expected:
        raise ValueError("APK checksum does not match release provenance")
    if manifest.get("tv_apk_sha256") != digest(directory / ASSETS[3]):
        raise ValueError("TV APK checksum does not match release provenance")
    # Older candidate metadata remains valid for promotion; new metadata names both updater targets.
    if "artifacts" in manifest:
        for target, name in (("phone", ASSETS[0]), ("tv", ASSETS[3])):
            if manifest["artifacts"].get(target) != {"file": name, "sha256": digest(directory / name)}:
                raise ValueError("APK target metadata does not match release provenance")
    assets = release_assets(directory)
    if manifest.get('inventory') is not None:
        expected_inventory = {'file': 'sbom-manifest.json', 'sha256': digest(directory / 'sbom-manifest.json')}
        if manifest['inventory'] != expected_inventory or len(assets) == len(ASSETS):
            raise ValueError('SBOM manifest provenance mismatch')
        verify_bundle(directory, manifest['source_sha'], phone=directory / ASSETS[0], tv=directory / ASSETS[3])
    elif len(assets) != len(ASSETS):
        raise ValueError('SBOM bundle missing release provenance')
    sums = (directory / "SHA256SUMS").read_text()
    required = "".join(f"{digest(directory / name)}  {name}\n"
                       for name in (ASSETS[0], ASSETS[2], ASSETS[3]))
    if sums != required:
        raise ValueError("Release checksum file does not match the assets")
    return manifest


def download(repo, tag, directory):
    run("gh", "release", "download", tag, "--repo", repo, "--dir", str(directory),
        *(argument for name in ASSETS for argument in ("--pattern", name)))
    entry = next((r for r in releases(repo) if r['tag_name'] == tag), None)
    if entry is None:
        raise ValueError('Release disappeared while downloading verified assets')
    names = {a['name'] for a in entry.get('assets', [])}
    if names & set(SBOM_ASSETS):
        if not set(SBOM_ASSETS) <= names:
            raise ValueError('Incomplete remote SBOM assets')
        run('gh', 'release', 'download', tag, '--repo', repo, '--dir', str(directory),
            *(argument for name in SBOM_ASSETS for argument in ('--pattern', name)))


def publish(repo, tag, sha, directory, notes, prerelease, publish_now=True, *, replace=False):
    assets = release_assets(directory)
    existing = [r for r in releases(repo) if r["tag_name"] == tag]
    if existing and not replace:
        # GitHub may defer creating a draft's tag until publication.
        tagged_sha = (existing[0].get("target_commitish") if existing[0]["draft"]
                      else api(f"repos/{repo}/commits/{tag}")["sha"])
        if tagged_sha != sha:
            raise ValueError("Existing release tag points to a different source commit")
    if existing and not replace and not existing[0]["draft"]:
        # A rerun may verify a published release, but must never replace its assets.
        with tempfile.TemporaryDirectory() as temp:
            remote = Path(temp)
            download(repo, tag, remote)
            if any(digest(remote / name) != digest(directory / name) for name in assets):
                raise ValueError("Published release differs; refusing to overwrite it")
        return
    if replace:
        if existing:
            run("gh", "release", "delete", tag, "--repo", repo, "--yes", "--cleanup-tag")
        elif remote_tag_exists(repo, tag):
            run("gh", "api", "--method", "DELETE", f"repos/{repo}/git/refs/tags/{tag}")
        existing = []
    with tempfile.TemporaryDirectory() as temp:
        body = Path(temp) / "notes.md"
        body.write_text(notes)
        if existing:
            run("gh", "release", "edit", tag, "--repo", repo, "--title", tag.lower(), "--notes-file", str(body))
        else:
            run("gh", "release", "create", tag, "--repo", repo, "--target", sha,
                "--title", tag.lower(), "--notes-file", str(body), "--draft")
        run("gh", "release", "upload", tag, "--repo", repo,
            *(str(directory / name) for name in assets), "--clobber")
        # Verify uploaded bytes before exposing the draft as a public release.
        remote = Path(temp) / "verify"
        remote.mkdir()
        download(repo, tag, remote)
        if any(digest(remote / name) != digest(directory / name) for name in assets):
            raise ValueError("Uploaded release assets did not match local bytes")
        if not publish_now:
            return
        run("gh", "release", "edit", tag, "--repo", repo, "--draft=false",
            f"--prerelease={'true' if prerelease else 'false'}",
            f"--latest={'false' if prerelease else 'true'}")


def remote_tag_exists(repo, tag):
    """Treat only an explicit API 404 as absence; network/auth failures block builds."""
    result = subprocess.run(["gh", "api", f"repos/{repo}/git/ref/tags/{tag}"],
                            text=True, capture_output=True, check=False)
    if result.returncode == 0:
        return True
    try:
        response = json.loads(result.stdout)
    except json.JSONDecodeError:
        response = {}
    if str(response.get("status")) == "404":
        return False
    raise subprocess.CalledProcessError(result.returncode, result.args, result.stdout, result.stderr)


def check_nightly(repo, sha, day):
    """Verify exact-source retries; different sources proceed to the full replacement build."""
    day = release_day(day)
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("Nightly source must be an exact commit")
    tag = f"{day}-nightly"
    existing = next((entry for entry in releases(repo) if entry["tag_name"] == tag), None)
    if existing and not existing["draft"] and release_source(existing) == sha:
        if api(f"repos/{repo}/commits/{tag}")["sha"] != sha:
            raise ValueError("Nightly tag does not match its source")
        with tempfile.TemporaryDirectory() as temp:
            download(repo, tag, Path(temp))
            verify_assets(Path(temp), sha)
        require_tested_image(repo, sha)
    return existing


def plan_nightly(repo, sha, day):
    """Reuse exact-source Nightlies or allocate a higher code for a validated replacement."""
    entry = check_nightly(repo, sha, day)
    same_source = bool(entry and not entry["draft"] and release_source(entry) == sha)
    print(f"existing={'true' if same_source else 'false'}")
    print(f"tag={release_day(day)}-nightly")
    print("stage=nightly")
    print(f"release_date={release_day(day)}")
    print(f"version_code={tag_version_code(entry) if same_source else next_version_code(releases(repo))}")


def require_tested_image(repo, sha):
    if "@sha256:" not in image_source(repo, sha):
        raise ValueError("Direct Stable requires a validated image digest for the exact source")


def latest_nightly(repo, main_sha):
    """Select the newest published Nightly whose exact source is in main history."""
    if not re.fullmatch(r"[0-9a-f]{40}", main_sha):
        raise ValueError("Main revision must be an exact commit SHA")
    candidates = []
    for entry in releases(repo):
        parsed = stage_tag(entry["tag_name"])
        if entry["draft"] or not entry["prerelease"] or not parsed or parsed[1] != "nightly":
            continue
        source = release_source(entry)
        if not source or not re.fullmatch(r"[0-9a-f]{40}", source):
            continue
        if subprocess.run(["git", "merge-base", "--is-ancestor", source, main_sha], check=False).returncode != 0:
            continue
        if api(f"repos/{repo}/commits/{entry['tag_name']}")["sha"] != source:
            raise ValueError("Nightly release tag and source provenance differ")
        with tempfile.TemporaryDirectory() as temp:
            download(repo, entry["tag_name"], Path(temp))
            verify_assets(Path(temp), source)
        require_tested_image(repo, source)
        candidates.append((parsed[0], entry.get("published_at", ""), entry, source))
    if not candidates:
        raise ValueError("No published, verified Nightly source is reachable from main")
    _, _, entry, source = max(candidates, key=lambda item: (item[0], item[1]))
    return entry, source


def plan_stable(repo, sha, day, predecessor_tag=None):
    """Allocate fresh Stable APKs from a tested Nightly without creating an RC."""
    day = release_day(day)
    require_tested_image(repo, sha)
    entries = releases(repo)
    tag = f"{day}-stable"
    existing = next((entry for entry in entries if stage_tag(entry["tag_name"]) == (day, "stable", 0)), None)
    draft = existing if existing and existing["draft"] else None
    if existing is None and remote_tag_exists(repo, tag):
        raise ValueError("Stable tag exists without its release; investigate before rebuilding")
    predecessor = next((entry for entry in entries
                        if entry["tag_name"] == predecessor_tag), None) if predecessor_tag else None
    if predecessor is None:
        predecessor = next((entry for entry in entries
                            if not entry["draft"] and entry["prerelease"]
                            and stage_tag(entry["tag_name"]) == (day, "nightly", 0)
                            and release_source(entry) == sha), None)
    if predecessor is None:
        raise ValueError("Stable requires a matching published Nightly release")
    with tempfile.TemporaryDirectory() as temp:
        download(repo, predecessor["tag_name"], Path(temp))
        prior = verify_assets(Path(temp), sha)
    if not predecessor["prerelease"] or stage_tag(predecessor["tag_name"]) != (day, "nightly", 0):
        raise ValueError("Stable predecessor must be the same-date Nightly release")
    code = max(next_version_code(entries), prior["version_code"] + 1,
               tag_version_code(draft) + 1 if draft else 0)
    print("existing=false")
    print(f"version_code={code}")
    print(f"tag={tag}")
    print("stage=stable")
    print(f"release_date={day}")
    print(f"source_sha={sha}")
    print(f"predecessor={predecessor['tag_name']}")


def plan_latest_stable(repo, main_sha):
    """Allocate Stable from the newest validated Nightly in the main history."""
    entry, source = latest_nightly(repo, main_sha)
    day = stage_tag(entry["tag_name"])[0]
    plan_stable(repo, source, day, predecessor_tag=entry["tag_name"])


def candidate(repo, tag, sha, code, directory, *, direct_stable=False, predecessor=None, nightly=False):
    parsed = stage_tag(tag)
    if parsed is not None and not tag.startswith(parsed[0] + "-"):
        raise ValueError("New candidate tags must use YY.MM.DD")
    allowed = {"nightly"} if nightly else {"stable"} if direct_stable else set()
    if parsed is None or parsed[1] not in allowed or not 0 < code <= 2100000000:
        raise ValueError("Invalid Android release version")
    if direct_stable or nightly:
        require_tested_image(repo, sha)
    manifest = {"source_sha": sha, "version_name": tag, "version_code": code,
                "package": "io.github.alreadyonapp.alreadyon",
                "apk_sha256": digest(directory / ASSETS[0]),
                "tv_apk_sha256": digest(directory / ASSETS[3]),
                "artifacts": {
                    "phone": {"file": ASSETS[0], "sha256": digest(directory / ASSETS[0])},
                    "tv": {"file": ASSETS[3], "sha256": digest(directory / ASSETS[3])}}}
    if len(release_assets(directory)) > len(ASSETS):
        verify_bundle(directory, sha, phone=directory / ASSETS[0], tv=directory / ASSETS[3])
        manifest['inventory'] = {'file': 'sbom-manifest.json', 'sha256': digest(directory / 'sbom-manifest.json')}
    (directory / ASSETS[2]).write_text(json.dumps(manifest, indent=2) + "\n")
    (directory / ASSETS[1]).write_text("".join(
        f"{digest(directory / name)}  {name}\n" for name in (ASSETS[0], ASSETS[2], ASSETS[3])))
    verify_assets(directory, sha)
    entries = releases(repo)
    baseline_tag = changelog_baseline(entries, tag)
    changes = (promotion_changes(predecessor.get("body") or "") if predecessor and release_source(predecessor) == sha
               else candidate_changes(repo, entries, tag, sha, baseline_tag,
                                      allow_source_replacement=nightly))
    notes = (release_body(repo, tag, sha, code, changes, candidate_tag=predecessor["tag_name"])
             if predecessor else release_body(repo, tag, sha, code, changes, changelog_baseline=baseline_tag))
    publish_now = bool(predecessor) or parsed[1] in {"nightly", "stable"}
    publish(repo, tag, sha, directory, notes, not direct_stable, publish_now=publish_now,
            replace=nightly or direct_stable)


def release_source(entry):
    match = re.search(r"Source commit: `([0-9a-f]{40})`", entry.get("body") or "")
    return match[1] if match else None


def publish_stable(repo, tag, sha, code, directory, predecessor_tag):
    """Publish fresh Stable APKs from the selected Nightly and preserve its notes."""
    predecessor = next((entry for entry in releases(repo) if entry["tag_name"] == predecessor_tag), None)
    if predecessor is None:
        raise ValueError("Selected Nightly predecessor release was not found")
    if predecessor["draft"] or stage_tag(predecessor_tag) is None or stage_tag(predecessor_tag)[1] != "nightly":
        raise ValueError("Stable predecessor must be a published Nightly")
    with tempfile.TemporaryDirectory() as temp:
        download(repo, predecessor_tag, Path(temp))
        prior = verify_assets(Path(temp), release_source(predecessor))
    if code <= prior["version_code"]:
        raise ValueError("Promotion APK version code must increase")
    candidate(repo, tag, sha, code, directory, direct_stable=True, predecessor=predecessor)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=("check-nightly", "plan-nightly", "nightly", "plan-stable", "plan-latest-stable", "publish-stable"))
    parser.add_argument("--repo", default=os.environ.get("GITHUB_REPOSITORY"), required=False)
    parser.add_argument("--sha")
    parser.add_argument("--day")
    parser.add_argument("--tag")
    parser.add_argument("--version-code", type=int)
    parser.add_argument("--directory", type=Path)
    parser.add_argument("--require-inventory", action="store_true")
    parser.add_argument("--predecessor")
    args = parser.parse_args()
    if args.require_inventory and not (args.directory / "sbom-manifest.json").exists():
        parser.error("New candidates require public inventory SBOMs")
    if not args.repo:
        parser.error("--repo or GITHUB_REPOSITORY is required")
    if args.operation in {"check-nightly", "plan-nightly"}:
        if not args.sha or not args.day:
            parser.error("Nightly planning requires --sha and --day")
        if args.operation == "check-nightly":
            entry = check_nightly(args.repo, args.sha, args.day)
            same_source = bool(entry and not entry["draft"] and release_source(entry) == args.sha)
            print(f"existing={'true' if same_source else 'false'}")
        else:
            plan_nightly(args.repo, args.sha, args.day)
    elif args.operation == "plan-stable":
        if not args.sha or not args.day:
            parser.error("Stable planning requires --sha and --day")
        plan_stable(args.repo, args.sha, args.day, predecessor_tag=args.predecessor)
    elif args.operation == "plan-latest-stable":
        if not args.sha:
            parser.error("plan-latest-stable requires the current main SHA")
        plan_latest_stable(args.repo, args.sha)
    elif args.operation == "publish-stable":
        if not all((args.sha, args.tag, args.version_code, args.directory, args.predecessor)):
            parser.error("publish-stable requires source, version, assets and Nightly predecessor")
        publish_stable(args.repo, args.tag, args.sha, args.version_code, args.directory, args.predecessor)
    elif args.operation == "nightly":
        if not all((args.tag, args.sha, args.version_code, args.directory)):
            parser.error("nightly requires --tag, --sha, --version-code and --directory")
        candidate(args.repo, args.tag, args.sha, args.version_code, args.directory,
                  nightly=True)


if __name__ == "__main__":
    main()
