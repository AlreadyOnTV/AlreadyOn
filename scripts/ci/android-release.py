#!/usr/bin/env python3
"""Publish signed Android candidates and copy tested assets for stable promotion."""
import argparse
import sys
from datetime import date, datetime
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time
from zoneinfo import ZoneInfo
sys.path.insert(0, str(Path(__file__).parents[1] / 'security'))
from public_inventory import NAMES as SBOM_NAMES, verify_bundle
SBOM_ASSETS = (*SBOM_NAMES, 'sbom-manifest.json')

from release_dates import release_day
from release_notes import changelog, changelog_since, promotion_changes, release_body

# New lifecycle: alphaN on dev, RCN on test, Stable on main. Accept old tags for history.
ALPHA = re.compile(r"((?:\d{2}\.\d{2}\.\d{2}|(?:20)?\d{2}-\d{2}-\d{2}))-alpha([1-9]\d*)\Z")
RC = re.compile(r"((?:\d{2}\.\d{2}\.\d{2}|(?:20)?\d{2}-\d{2}-\d{2}))-RC([1-9]\d*)\Z")
STABLE = re.compile(r"((?:\d{2}\.\d{2}\.\d{2}|(?:20)?\d{2}-\d{2}-\d{2}))-Stable\Z")
LEGACY = re.compile(r"((?:\d{2}\.\d{2}\.\d{2}|(?:20)?\d{2}-\d{2}-\d{2}))-(beta|rc|stable)([1-9]\d*)?\Z")
LEGACY_BETA = re.compile(r"((?:\d{2}\.\d{2}\.\d{2}|(?:20)?\d{2}-\d{2}-\d{2}))-beta([1-9]\d*)?\Z")
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
        rank = {"alpha": 0, "beta": 1, "rc": 2, "stable": 3}[parsed[1]]
        candidates.append((parsed[0], rank, parsed[2], entry["tag_name"]))
    return max(candidates)[3] if candidates else None


def next_dev_tag(entries, day):
    """Allocate alphaN before RC1; after RC1, draft the next RC instead."""
    parsed = [(entry, stage_tag(entry["tag_name"])) for entry in entries]
    if any(tag and tag[0] == day and tag[1] == "stable" and not entry.get("draft", False)
           for entry, tag in parsed):
        raise ValueError(f"{day} is stable; start a new release date")
    rcs = [tag[2] for entry, tag in parsed
           if tag and tag[0] == day and tag[1] == "rc" and not entry.get("draft", False)]
    if rcs:
        return f"{day}-RC{max(rcs) + 1}"
    alphas = [tag[2] for entry, tag in parsed
              if tag and tag[0] == day and tag[1] == "alpha" and not entry.get("draft", False)]
    return f"{day}-alpha{max(alphas, default=0) + 1}"


def cleanup_legacy_betas(repo, day):
    """Remove superseded legacy beta tags for this date, never earlier RC history."""
    removed = []
    for entry in releases(repo):
        match = LEGACY_BETA.fullmatch(entry["tag_name"])
        if match is None or release_day(match[1], historical=True) != day:
            continue
        run("gh", "release", "delete", entry["tag_name"], "--repo", repo,
            "--yes", "--cleanup-tag")
        removed.append(entry["tag_name"])
    return removed


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
        before = (previous[1] == "alpha" and current[1] == "alpha" and previous[2] < current[2]
                  or previous[1] == "alpha" and current[1] == "rc"
                  or previous[1] == "rc" and current[1] == "rc" and previous[2] < current[2])
        if before:
            rank = {"alpha": 0, "rc": 1}[previous[1]]
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
    if not CANDIDATE.fullmatch(manifest.get("version_name", "")):
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


def publish(repo, tag, sha, directory, notes, prerelease, publish_now=True):
    assets = release_assets(directory)
    existing = [r for r in releases(repo) if r["tag_name"] == tag]
    if existing:
        # GitHub may defer creating a draft's tag until publication.
        tagged_sha = (existing[0].get("target_commitish") if existing[0]["draft"]
                      else api(f"repos/{repo}/commits/{tag}")["sha"])
        if tagged_sha != sha:
            raise ValueError("Existing release tag points to a different source commit")
    if existing and not existing[0]["draft"]:
        # A rerun may verify a published release, but must never replace its assets.
        with tempfile.TemporaryDirectory() as temp:
            remote = Path(temp)
            download(repo, tag, remote)
            if any(digest(remote / name) != digest(directory / name) for name in assets):
                raise ValueError("Published release differs; refusing to overwrite it")
        return
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


def plan(repo, sha, day=None):
    day = release_day(day or datetime.now(ZoneInfo("America/New_York")).date().strftime('%y.%m.%d'))
    entries = releases(repo)
    marker = f"Source commit: `{sha}`"
    existing = next((entry for entry in entries
                     if (ALPHA.fullmatch(entry["tag_name"]) or RC.fullmatch(entry["tag_name"]))
                     and marker in (entry.get("body") or "")), None)
    if existing and not existing["draft"]:
        target = api(f"repos/{repo}/commits/{existing['tag_name']}")["sha"]
        if target != sha:
            raise ValueError("Existing candidate tag does not match its recorded source")
        with tempfile.TemporaryDirectory() as temp:
            download(repo, existing["tag_name"], Path(temp))
            verify_assets(Path(temp), sha)
        print("existing=true")
        print(f"tag={existing['tag_name']}")
        print(f"stage={'alpha' if ALPHA.fullmatch(existing['tag_name']) else 'rc'}")
        print(f"release_date={day}")
        return
    tag = next_dev_tag(entries, day)
    draft = next((entry for entry in entries if entry["tag_name"] == tag and entry["draft"]), None)
    if draft:
        if existing and existing["tag_name"] == tag:
            version_code = tag_version_code(draft)
            if set(ASSETS).issubset({asset["name"] for asset in draft.get("assets", [])}):
                with tempfile.TemporaryDirectory() as temp:
                    download(repo, tag, Path(temp))
                    verify_assets(Path(temp), sha)
                print("existing=true")
                print(f"tag={tag}")
                print("stage=rc")
                print(f"release_date={day}")
                return
        else:
            run("gh", "release", "delete", tag, "--repo", repo, "--yes", "--cleanup-tag")
            # One unpublished RC draft may be refreshed as a hotfix advances before test promotion.
            version_code = int(time.time()) - 1577836800
    else:
        version_code = int(time.time()) - 1577836800
    tags = run("git", "tag", "--list").splitlines()
    if tag in tags and not draft:
        raise ValueError(f"Git tag {tag} exists without a matching release; investigate before rebuilding")
    print("existing=false")
    print(f"tag={tag}")
    print(f"version_code={version_code}")
    print(f"stage={'alpha' if ALPHA.fullmatch(tag) else 'rc'}")
    print(f"release_date={day}")


def candidate(repo, tag, sha, code, directory):
    parsed = stage_tag(tag)
    if parsed is not None and not tag.startswith(parsed[0] + "-"):
        raise ValueError("New candidate tags must use YY.MM.DD")
    if parsed is None or parsed[1] not in {"alpha", "rc", "beta"} or not 0 < code <= 2100000000:
        raise ValueError("Invalid Android release version")
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
    if baseline_tag:
        baseline_sha = api(f"repos/{repo}/commits/{baseline_tag}")["sha"]
        changes = changelog_since(sha, baseline_sha)
    else:
        stable_tags = [entry['tag_name'] for entry in entries
                       if not entry['draft'] and not entry['prerelease']]
        changes = changelog(sha, stable_tags)
    notes = release_body(repo, tag, sha, code, changes, changelog_baseline=baseline_tag)
    publish_now = parsed[1] in {"alpha", "beta"}
    publish(repo, tag, sha, directory, notes, True, publish_now=publish_now)


def release_source(entry):
    match = re.search(r"Source commit: `([0-9a-f]{40})`", entry.get("body") or "")
    return match[1] if match else None


def promote_test(repo, day, sha):
    """Publish the next RC by reusing the matching alpha or already-built RC draft."""
    entries = releases(repo)
    published_rcs = [(int(match[2]), entry) for entry in entries
                     if not entry["draft"] and (match := RC.fullmatch(entry["tag_name"]))
                     and match[1] == day]
    already_published = next((entry for _, entry in published_rcs if release_source(entry) == sha), None)
    if already_published:
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            download(repo, already_published["tag_name"], directory)
            verify_assets(directory, sha)
        print(f"Verified existing {already_published['tag_name']} without rebuilding APKs")
        return already_published["tag_name"]

    tag = f"{day}-RC{max((number for number, _ in published_rcs), default=0) + 1}"
    draft = next((entry for entry in entries if entry["tag_name"] == tag and entry["draft"]), None)
    if draft:
        if release_source(draft) != sha:
            raise ValueError(f"Draft {tag} does not contain the selected dev source")
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            download(repo, tag, directory)
            verify_assets(directory, sha)
        run("gh", "release", "edit", tag, "--repo", repo, "--draft=false",
            "--prerelease=true", "--latest=false")
        print(f"Published {tag} from its existing draft without rebuilding APKs")
        return tag

    alphas = [(int(match[2]), entry) for entry in entries
              if not entry["draft"] and (match := ALPHA.fullmatch(entry["tag_name"]))
              and match[1] == day and release_source(entry) == sha]
    if not alphas:
        raise ValueError("No published alpha artifacts match the selected dev source")
    _, alpha = max(alphas, key=lambda item: item[0])
    alpha_tag = alpha["tag_name"]
    with tempfile.TemporaryDirectory() as temp:
        directory = Path(temp)
        download(repo, alpha_tag, directory)
        manifest = verify_assets(directory, sha)
        notes = release_body(repo, tag, sha, manifest["version_code"],
                             promotion_changes(alpha.get("body") or ""),
                             candidate_tag=alpha_tag)
        publish(repo, tag, sha, directory, notes, True)
    print(f"Published {tag} from {alpha_tag} using the same signed APK bytes")
    return tag


def promote_stable(repo, day, selected_sha):
    """Promote the newest published RC in the selected source history without rebuilding."""
    candidates = []
    for entry in releases(repo):
        match = RC.fullmatch(entry["tag_name"])
        source = release_source(entry)
        if entry["draft"] or match is None or release_day(match[1], historical=True) != day or not source:
            continue
        ancestry = subprocess.run(["git", "merge-base", "--is-ancestor", source, selected_sha], check=False)
        if ancestry.returncode == 0:
            candidates.append((int(match[2]), entry, source))
        elif ancestry.returncode != 1:
            raise subprocess.CalledProcessError(ancestry.returncode, ancestry.args)
    if not candidates:
        raise ValueError("No published RC for this date is in the selected main source history")
    _, candidate, artifact_sha = max(candidates, key=lambda item: item[0])
    stable = f"{day}-Stable"
    with tempfile.TemporaryDirectory() as temp:
        directory = Path(temp)
        download(repo, candidate["tag_name"], directory)
        manifest = verify_assets(directory, artifact_sha)
        notes = release_body(repo, stable, selected_sha, manifest["version_code"],
                             promotion_changes(candidate.get("body") or ""),
                             candidate_tag=candidate["tag_name"])
        publish(repo, stable, selected_sha, directory, notes, False)
    print(f"Published {stable} from {candidate['tag_name']} without rebuilding APKs")
    return stable


def promote(repo, tag):
    """Retain the manual single-candidate stable promotion interface for legacy callers."""
    parsed = stage_tag(tag)
    if parsed is None or parsed[1] not in {"alpha", "rc", "beta"}:
        raise ValueError("Stable promotion requires a dated alpha or RC candidate")
    release = api(f"repos/{repo}/releases/tags/{tag}")
    if release["draft"] or not release["prerelease"]:
        raise ValueError("Select a published Android release candidate")
    sha = run("git", "rev-parse", f"refs/tags/{tag}^{{commit}}")
    subprocess.run(["git", "merge-base", "--is-ancestor", sha, "origin/main"], check=True)
    return promote_stable(repo, parsed[0], sha)


def promote_selected_test(repo, day, sha):
    return promote_test(repo, day, sha)


def promote_selected_stable(repo, day, sha):
    return promote_stable(repo, day, sha)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=("plan", "candidate", "promote", "promote-test", "promote-stable", "cleanup-legacy"))
    parser.add_argument("--repo", default=os.environ.get("GITHUB_REPOSITORY"), required=False)
    parser.add_argument("--sha")
    parser.add_argument("--day")
    parser.add_argument("--tag")
    parser.add_argument("--version-code", type=int)
    parser.add_argument("--directory", type=Path)
    parser.add_argument("--require-inventory", action="store_true")
    args = parser.parse_args()
    if args.require_inventory and not (args.directory / "sbom-manifest.json").exists():
        parser.error("New candidates require public inventory SBOMs")
    if not args.repo:
        parser.error("--repo or GITHUB_REPOSITORY is required")
    if args.operation == "plan":
        if not args.sha:
            parser.error("plan requires --sha")
        plan(args.repo, args.sha, args.day)
    elif args.operation == "candidate":
        if not all((args.tag, args.sha, args.version_code, args.directory)):
            parser.error("candidate requires --tag, --sha, --version-code and --directory")
        candidate(args.repo, args.tag, args.sha, args.version_code, args.directory)
    elif args.operation == "promote":
        if not args.tag:
            parser.error("promote requires --tag")
        promote(args.repo, args.tag)
    elif args.operation == "promote-test":
        if not all((args.day, args.sha)):
            parser.error("promote-test requires --day and --sha")
        promote_selected_test(args.repo, args.day, args.sha)
    elif args.operation == "promote-stable":
        if not all((args.day, args.sha)):
            parser.error("promote-stable requires --day and --sha")
        promote_selected_stable(args.repo, args.day, args.sha)
    else:
        if not args.day:
            parser.error("cleanup-legacy requires --day")
        removed = cleanup_legacy_betas(args.repo, args.day)
        print("Removed legacy same-day betas: " + (", ".join(removed) if removed else "none"))


if __name__ == "__main__":
    main()
