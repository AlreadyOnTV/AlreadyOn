#!/usr/bin/env python3
"""Fast-forward a tested date release to test or main without rebuilding."""
import argparse
import importlib.util
from pathlib import Path
import re
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parents[1]
CI = ROOT / 'scripts/ci'
sys.path.insert(0, str(CI))


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def ancestor(older, newer):
    return subprocess.run(['git', 'merge-base', '--is-ancestor', older, newer], check=False).returncode == 0


def validate_fast_forward(target_head, selected, upstream, target):
    if not ancestor(selected, upstream):
        raise ValueError(f'Selected commit is not in current {upstream} history')
    if not ancestor(selected, target_head) and not ancestor(target_head, selected):
        raise ValueError(f'{target} and selected release have diverged; reconcile the release on dev')


def promotion_commit(target, target_head, selected, original_branch):
    """Fast-forward or create an exact-tree merge commit, restoring the caller's branch."""
    if run('git', 'status', '--porcelain'):
        raise ValueError('Working tree must be clean before branch promotion')
    if ancestor(target_head, selected):
        return selected
    result = None
    switched = False
    try:
        subprocess.run(['git', 'switch', '--detach', target_head], check=True)
        switched = True
        subprocess.run(['git', 'merge', '--no-ff', '--no-edit', selected], check=True)
        result = run('git', 'rev-parse', 'HEAD')
        if run('git', 'rev-parse', f'{result}^{{tree}}') != run('git', 'rev-parse', f'{selected}^{{tree}}'):
            raise ValueError('Promotion merge changed the tested source tree; resolve changes on dev first')
        return result
    except Exception:
        if switched and subprocess.run(['git', 'rev-parse', '--verify', 'MERGE_HEAD'],
                                       capture_output=True, check=False).returncode == 0:
            subprocess.run(['git', 'merge', '--abort'], check=False)
        raise
    finally:
        if switched:
            subprocess.run(['git', 'switch', original_branch], check=True)


def load(name):
    spec = importlib.util.spec_from_file_location(name, CI / f'{name}.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def ensure_image_record(repo, sha):
    registry = load('image_registry')
    source = registry.release_source(repo, sha)
    if '@sha256:' not in source:
        raise ValueError(f'No validated image digest is recorded for {sha}')


def ensure_rc(repo, day, selected):
    release = load('android-release')
    entries = release.releases(repo)
    matches = []
    for entry in entries:
        match = release.RC.fullmatch(entry['tag_name'])
        source = release.release_source(entry)
        if entry['draft'] or not entry['prerelease'] or not match or match[1] != day or not source:
            continue
        if subprocess.run(['git', 'merge-base', '--is-ancestor', source, selected], check=False).returncode == 0:
            matches.append((int(match[2]), entry, source))
    if not matches:
        raise ValueError(f'No published {day}-RC release matches the tested source history')
    _, entry, source = max(matches, key=lambda item: item[0])
    with tempfile.TemporaryDirectory(prefix='alreadyon-promotion-') as temp:
        assets = Path(temp)
        release.download(repo, entry['tag_name'], assets)
        release.verify_assets(assets, source)


def main():
    """Advance test/main only by fast-forward to artifacts already validated on dev."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('target', choices=['test', 'main'])
    parser.add_argument('commit', nargs='?', help='Exact validated dev commit for test; main uses current test head')
    args = parser.parse_args()
    if (args.target == 'test') != bool(args.commit):
        parser.error('test requires a selected dev commit; main always selects the current test head')
    repo = run('gh', 'repo', 'view', '--json', 'nameWithOwner', '--jq', '.nameWithOwner')
    run('git', 'fetch', '--prune', 'origin')
    upstream_name = 'dev' if args.target == 'test' else 'test'
    target_head = run('git', 'rev-parse', f'origin/{args.target}')
    upstream_head = run('git', 'rev-parse', f'origin/{upstream_name}')
    source_ref = args.commit or f'origin/{upstream_name}'
    if args.target == 'test' and re.fullmatch(r'\d{2}\.\d{2}\.\d{2}', source_ref):
        source_ref = f'refs/tags/{source_ref}'
    selected = run('git', 'rev-parse', '--verify', source_ref + '^{commit}')
    if args.target == 'main' and selected != upstream_head:
        raise ValueError('Main promotion must use the current test head')
    validate_fast_forward(target_head, selected, upstream_head, args.target)
    ensure_image_record(repo, selected)
    context = load('release-context')
    day = context.date_for_source(selected)
    release = load('android-release')
    stable_ready = release.published_stable(repo, day, selected) is not None
    if stable_ready:
        print(f'Verified exact-source {day} Stable APKs; no candidate release is needed')
    elif args.target == 'test':
        date_tag = run('git', 'rev-parse', f'refs/tags/{day}^{{commit}}')
        if date_tag != selected:
            raise ValueError(f'Date tag {day} does not point to the selected dev commit')
        candidate = next((entry for entry in release.releases(repo)
                          if (match := (release.ALPHA.fullmatch(entry['tag_name'])
                                        or release.RC.fullmatch(entry['tag_name'])))
                          and match[1] == day and release.release_source(entry) == selected
                          and (not entry['draft'] or release.RC.fullmatch(entry['tag_name']))), None)
        if candidate is None:
            raise ValueError('No published alpha or draft RC APKs match the selected dev source')
        with tempfile.TemporaryDirectory(prefix='alreadyon-promotion-') as temp:
            assets = Path(temp)
            release.download(repo, candidate['tag_name'], assets)
            release.verify_assets(assets, selected)
    else:
        ensure_rc(repo, day, selected)
    original_branch = run('git', 'branch', '--show-current')
    promoted = promotion_commit(args.target, target_head, selected, original_branch)
    subprocess.run(['git', 'push', 'origin', f'{promoted}:refs/heads/{args.target}'], check=True)
    print(f'Promoted tested source {selected} to {args.target} as {promoted}; the existing digest will be retagged')


if __name__ == '__main__':
    main()
