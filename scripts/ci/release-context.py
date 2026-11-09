#!/usr/bin/env python3
"""Resolve today's release date and create its immutable source tag."""
import argparse
from datetime import datetime, timezone
import json
import os
import re
import subprocess
from zoneinfo import ZoneInfo
from release_dates import release_day

SHA = re.compile(r'[0-9a-f]{40}')


def valid_date(value):
    return release_day(value)


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def resolve_date(revision, explicit=None, now=None):
    if not SHA.fullmatch(revision):
        raise ValueError('Expected a full source commit SHA')
    if explicit:
        return valid_date(explicit)
    current = now or datetime.now(timezone.utc)
    return current.astimezone(ZoneInfo('America/New_York')).strftime('%y.%m.%d')


def remote_ref(repository, ref):
    result = subprocess.run(['gh', 'api', f'repos/{repository}/git/ref/{ref}'],
                            capture_output=True, text=True)
    if result.returncode:
        for output in (result.stdout, result.stderr):
            try:
                response = json.loads(output)
            except json.JSONDecodeError:
                continue
            if response.get('status') == '404' or response.get('message') == 'Not Found':
                return None
        raise subprocess.CalledProcessError(result.returncode, result.args, result.stdout, result.stderr)
    return json.loads(result.stdout)['object']['sha']


def update_source_tag(repository, day, revision):
    day = valid_date(day)
    if not SHA.fullmatch(revision):
        raise ValueError('Expected a full source commit SHA')
    current = remote_ref(repository, f'tags/{day}')
    if current == revision:
        return revision
    if current:
        payload = json.dumps({'sha': revision, 'force': True})
        subprocess.run(['gh', 'api', '--method', 'PATCH', f'repos/{repository}/git/refs/tags/{day}',
                        '--input', '-'], input=payload, text=True, check=True, stdout=subprocess.DEVNULL)
    else:
        payload = json.dumps({'ref': f'refs/tags/{day}', 'sha': revision})
        subprocess.run(['gh', 'api', '--method', 'POST', f'repos/{repository}/git/refs',
                        '--input', '-'], input=payload, text=True, check=True, stdout=subprocess.DEVNULL)
    return revision


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('date', 'tag'))
    parser.add_argument('--repo')
    parser.add_argument('--sha', required=True)
    parser.add_argument('--date')
    args = parser.parse_args()
    repository = args.repo or os.environ['GITHUB_REPOSITORY']
    day = resolve_date(args.sha, args.date)
    if args.command == 'tag':
        update_source_tag(repository, day, args.sha)
    elif args.command == 'cleanup-branch':
        cleanup_release_branch(repository, day, args.sha)
    print(day)


if __name__ == '__main__':
    main()
