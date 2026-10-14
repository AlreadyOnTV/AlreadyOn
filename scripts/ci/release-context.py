#!/usr/bin/env python3
"""Resolve a merged date-branch PR and maintain its movable source tag."""
import argparse
from pathlib import Path
from datetime import datetime
import json
import os
import re
import subprocess
from release_dates import release_day, HISTORICAL_DATE

SHA = re.compile(r'[0-9a-f]{40}')
DATE = re.compile(r'\d{2}-\d{2}-\d{2}')


def valid_date(value):
    return release_day(value)


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def resolve_date(repository, revision, explicit=None):
    if not SHA.fullmatch(revision):
        raise ValueError('Expected a full source commit SHA')
    if explicit:
        day = valid_date(explicit)
    else:
        pulls = json.loads(run('gh', 'api', f'repos/{repository}/commits/{revision}/pulls'))
        matches = {
            release_day(pr['head']['ref'], historical=True)
            for pr in pulls
            if pr.get('base', {}).get('ref') == 'dev'
            and pr.get('merged_at')
            and pr.get('merge_commit_sha') == revision
            and (DATE.fullmatch(pr.get('head', {}).get('ref', '')) or HISTORICAL_DATE.fullmatch(pr.get('head', {}).get('ref', '')))
        }
        if len(matches) != 1:
            raise ValueError('Dev release commit must come from exactly one merged YY-MM-DD PR')
        day = matches.pop()
    if remote_ref(repository, f'tags/{day}-Stable') or remote_ref(repository, f'tags/{day}-stable'):
        raise ValueError(f'{day} is stable; release a new date')
    return day


def date_for_source(revision):
    """Resolve the newest date tag reachable from a promoted source commit."""
    if not SHA.fullmatch(revision):
        raise ValueError('Expected a full source commit SHA')
    tags = subprocess.check_output(
        ['git', 'for-each-ref', '--format=%(refname:strip=2)', 'refs/tags'], text=True).splitlines()
    candidates = []
    for tag in tags:
        if not DATE.fullmatch(tag):
            continue
        try:
            valid_date(tag)
        except ValueError:
            continue
        result = subprocess.run(['git', 'merge-base', '--is-ancestor', f'refs/tags/{tag}', revision],
                                check=False)
        if result.returncode == 0:
            candidates.append(tag)
        elif result.returncode != 1:
            raise subprocess.CalledProcessError(result.returncode, result.args)
    if not candidates:
        raise ValueError('No YY-MM-DD release tag is reachable from the promoted source')
    return max(candidates)


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
    stable = remote_ref(repository, f'tags/{day}-Stable') or remote_ref(repository, f'tags/{day}-stable')
    if stable:
        raise ValueError(f'{day} is stable; its source tag is immutable')
    current = remote_ref(repository, f'tags/{day}')
    if current == revision:
        return revision
    if current:
        if not subprocess.run(['git', 'merge-base', '--is-ancestor', current, revision], check=False).returncode == 0:
            raise ValueError('New release source must descend from the existing date tag')
        payload = json.dumps({'sha': revision, 'force': False})
        subprocess.run(['gh', 'api', '--method', 'PATCH', f'repos/{repository}/git/refs/tags/{day}',
                        '--input', '-'], input=payload, text=True, check=True, stdout=subprocess.DEVNULL)
    else:
        payload = json.dumps({'ref': f'refs/tags/{day}', 'sha': revision})
        subprocess.run(['gh', 'api', '--method', 'POST', f'repos/{repository}/git/refs',
                        '--input', '-'], input=payload, text=True, check=True, stdout=subprocess.DEVNULL)
    return revision


def cleanup_release_branch(repository, day, revision):
    """Delete a merged date branch only after the release build and with no open PR."""
    day = valid_date(day)
    branch = remote_ref(repository, f'heads/{day}')
    if branch is None:
        return False
    if not subprocess.run(['git', 'merge-base', '--is-ancestor', branch, revision], check=False).returncode == 0:
        return False
    pulls = json.loads(subprocess.check_output(
        ['gh', 'pr', 'list', '--state', 'open', '--base', 'dev', '--head', day,
         '--json', 'number'], text=True))
    if pulls:
        return False
    subprocess.run(['gh', 'api', '--method', 'DELETE',
                    f'repos/{repository}/git/refs/heads/{day}'], check=True,
                   stdout=subprocess.DEVNULL)
    return True


def build_date(repository, revision, branch, configured_date, requested_date=None):
    """Permit dev releases and explicit current-cycle branches, never unrelated refs."""
    if branch != 'dev':
        active = valid_date(configured_date.strip())
        if branch != active:
            raise ValueError('Build must run on dev or the active release branch')
        if requested_date and requested_date != active:
            raise ValueError('Requested date must match the active release branch')
        requested_date = active
    return resolve_date(repository, revision, requested_date)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('date', 'build-date', 'date-for-source', 'tag', 'cleanup-branch'))
    parser.add_argument('--repo')
    parser.add_argument('--sha', required=True)
    parser.add_argument('--date')
    args = parser.parse_args()
    repository = args.repo or os.environ['GITHUB_REPOSITORY']
    if args.command == 'build-date':
        configured = (Path(__file__).resolve().parents[2] / 'release-date.txt').read_text()
        print(build_date(repository, args.sha, os.environ['GITHUB_REF_NAME'], configured, args.date))
        return
    day = (date_for_source(args.sha) if args.command == 'date-for-source'
           else resolve_date(repository, args.sha, args.date))
    if args.command == 'tag':
        update_source_tag(repository, day, args.sha)
    elif args.command == 'cleanup-branch':
        cleanup_release_branch(repository, day, args.sha)
    print(day)


if __name__ == '__main__':
    main()
