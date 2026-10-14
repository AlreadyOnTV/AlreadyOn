#!/usr/bin/env python3
"""Prepare the single local build branch and publish date-based dev PRs."""
import argparse
from datetime import datetime
import json
from pathlib import Path
import re
import subprocess
import sys
from zoneinfo import ZoneInfo
sys.path.insert(0, str(Path(__file__).resolve().parent / "ci"))
from release_dates import release_day as canonical_day


def git(*args):
    return subprocess.check_output(['git', *args], text=True).strip()


def ancestor(older, newer):
    return subprocess.run(['git', 'merge-base', '--is-ancestor', older, newer], check=False).returncode == 0


def release_day(value=None):
    day = value or datetime.now(ZoneInfo('America/New_York')).date().isoformat()[2:]
    return canonical_day(day)


def remote_ref_sha(ref, tags=False):
    kind = 'tags' if tags else 'heads'
    result = subprocess.check_output(['git', 'ls-remote', 'origin', f'refs/{kind}/{ref}',
                                      f'refs/{kind}/{ref}^{{}}'], text=True)
    refs = {}
    for line in result.splitlines():
        revision, name = line.split('\t', 1)
        refs[name] = revision
    return refs.get(f'refs/{kind}/{ref}^{{}}', refs.get(f'refs/{kind}/{ref}'))


def active_release_day(remote_tags, baseline=None):
    dates = {match[0] for name in remote_tags
             if (match := re.fullmatch(r'(\d{2}-\d{2}-\d{2})', name))}
    stable = {match[1] for name in remote_tags
              if (match := re.fullmatch(r'(\d{2}-\d{2}-\d{2})-(?:Stable|stable)', name))}
    # A maintainer can snap to a new cycle without deleting historical preview tags.
    if baseline:
        baseline = release_day(baseline)
        dates = {day for day in dates if day >= baseline}
        if baseline not in stable:
            dates.add(baseline)
    active = sorted(dates - stable)
    if len(active) > 1:
        raise ValueError('Multiple date releases are active; resolve their promotion state first')
    return active[0] if active else None


def release_tags():
    output = subprocess.check_output(['git', 'ls-remote', '--tags', 'origin'], text=True)
    return [line.split('\t', 1)[1].removeprefix('refs/tags/')
            for line in output.splitlines()
            if line.split('\t', 1)[1].startswith('refs/tags/')
            and not line.split('\t', 1)[1].endswith('^{}')]


def publish_release_branch(value=None):
    state = git('status', '--porcelain')
    if state:
        sys.exit('Commit local build changes before publishing the dated PR branch')
    if git('branch', '--show-current') != 'build':
        sys.exit('Switch to the local build branch before publishing a release PR')
    subprocess.run(['git', 'fetch', '--prune', 'origin'], check=True)
    head = git('rev-parse', 'HEAD')
    dev = git('rev-parse', 'origin/dev')
    if not ancestor(dev, head):
        sys.exit('Local build must include current origin/dev before opening its release PR')
    remote_tags = release_tags()
    baseline = Path(__file__).resolve().parents[1] / 'release-date.txt'
    active_day = active_release_day(remote_tags, baseline.read_text().strip() if baseline.exists() else None)
    day = release_day(value or active_day)
    if active_day and day != active_day:
        sys.exit(f'Release {active_day} must reach Stable before starting release {day}')
    stable_tags = [f'{day}-Stable', f'{day}-stable']
    if any(remote_ref_sha(tag, tags=True) for tag in stable_tags):
        sys.exit(f'{day} is already stable; start a new release date')

    branch_sha = remote_ref_sha(day)
    if branch_sha and not ancestor(branch_sha, head):
        sys.exit(f'origin/{day} contains commits not present in local build; refusing to overwrite it')
    subprocess.run(['git', 'push', 'origin', f'HEAD:refs/heads/{day}'], check=True)

    tag_sha = remote_ref_sha(day, tags=True)
    if tag_sha and not ancestor(tag_sha, head):
        sys.exit(f'tag {day} points to unrelated history; refusing to move it')
    subprocess.run(['git', 'tag', '-f', day, head], check=True)
    if tag_sha:
        subprocess.run(['git', 'push', f'--force-with-lease=refs/tags/{day}:{tag_sha}',
                        'origin', f'refs/tags/{day}'], check=True)
    else:
        subprocess.run(['git', 'push', 'origin', f'refs/tags/{day}'], check=True)

    existing = json.loads(subprocess.check_output(
        ['gh', 'pr', 'list', '--state', 'open', '--base', 'dev', '--head', day,
         '--json', 'number,url'], text=True))
    if existing:
        print(f'Updated release PR #{existing[0]["number"]}: {existing[0]["url"]}')
    else:
        body = (f'Release `{day}` from local build commit `{head}`.\n\n'
                'The PR runs temporary image validation, full e2e and code scanning. '
                'After it passes, merge to dev; the dev release workflow updates the date tag and publishes alpha artifacts.')
        subprocess.run(['gh', 'pr', 'create', '--base', 'dev', '--head', day,
                        '--title', f'Release {day}', '--body', body], check=True)


def main():
    """Start local work from dev without discarding a unique build commit."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['start', 'publish'])
    parser.add_argument('name_or_date', nargs='?')
    args = parser.parse_args()
    if args.command == 'publish':
        publish_release_branch(args.name_or_date)
        return
    if args.name_or_date not in (None, 'build'):
        parser.error('start only accepts the local build branch')
    state = subprocess.check_output(['git', 'status', '--porcelain'], text=True)
    if state:
        sys.exit('Commit existing changes before switching the shared checkout')
    subprocess.run(['git', 'fetch', '--prune', 'origin'], check=True)
    dev = git('rev-parse', 'origin/dev')
    exists = subprocess.run(['git', 'show-ref', '--verify', '--quiet', 'refs/heads/build']).returncode == 0
    if not exists:
        subprocess.run(['git', 'switch', '--no-track', '-c', 'build', dev], check=True)
    else:
        build = git('rev-parse', 'build')
        current = git('branch', '--show-current')
        if ancestor(dev, build):
            subprocess.run(['git', 'switch', 'build'], check=True)
            print('Resuming build branch with unmerged commits intact')
            return
        if ancestor(build, dev):
            if current == 'build':
                subprocess.run(['git', 'switch', 'dev'], check=True)
            subprocess.run(['git', 'branch', '-f', 'build', dev], check=True)
            subprocess.run(['git', 'switch', 'build'], check=True)
        else:
            subprocess.run(['git', 'switch', 'build'], check=True)
            subprocess.run(['git', 'merge', '--no-edit', dev], check=True)
            print('Merged current origin/dev into existing build work')
    print('Local build branch is ready from origin/dev; use the PR workflow when tested')


if __name__ == '__main__':
    main()
