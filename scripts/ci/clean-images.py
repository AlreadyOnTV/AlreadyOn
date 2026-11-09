#!/usr/bin/env python3
"""Plan GHCR version retention; --apply is required for deletion.

Versions, not individual aliases, are the GitHub deletion unit. Always preserve
channel roots and their manifest descendants before considering any deletion.
"""
import argparse
import datetime
import json
import os
import re
import subprocess
from zoneinfo import ZoneInfo
from image_registry import api
from release_dates import parse_day

CHANNELS = {'nightly', 'latest', 'stable'}
DATE = re.compile(r'(?:\d{2}\.\d{2}\.\d{2}|(?:20)?\d{2}-\d{2}-\d{2})')
NIGHTLY = re.compile(r'(\d{2}\.\d{2}\.\d{2})-nightly')
PR = re.compile(r'pr-(\d+)(?:-[0-9a-f]{40})?')
REBUILD = re.compile(r'rebuild-(\d+)-\d+')


def retain(tags, today, open_pr, active_run, recent_expired=False):
    """Return a protection reason, or None. Newest date alias defines expiry."""
    if CHANNELS.intersection(tags):
        return 'release channel'
    if recent_expired:
        return 'one of the five newest expired releases'
    dates = [parse_day(tag, historical=True) for tag in tags if DATE.fullmatch(tag)]
    dates += [parse_day(match[1]) for tag in tags if (match := NIGHTLY.fullmatch(tag))]
    if dates and max(dates) >= today - datetime.timedelta(days=90):
        return 'date within 90 days'
    for tag in tags:
        match = PR.fullmatch(tag)
        if match and open_pr(int(match[1])):
            return 'open PR'
        match = REBUILD.fullmatch(tag)
        if match and active_run(int(match[1])):
            return 'active rebuild'
        # Legacy bases must not disappear while old branches still depend on them.
        if tag.startswith('base-'):
            return 'legacy base (migration requires separate approval)'
        if not (DATE.fullmatch(tag) or NIGHTLY.fullmatch(tag) or PR.fullmatch(tag) or REBUILD.fullmatch(tag)
                or re.fullmatch(r'sha-[0-9a-f]{40}', tag)):
            return 'unrecognized tag; review required'
    return None


def recent_expired_versions(versions, today, days=90, limit=5):
    """Keep the newest dated image versions that are already beyond the age window."""
    dated = []
    for version in versions:
        tags = version['metadata']['container']['tags']
        dates = [parse_day(tag, historical=True) for tag in tags if DATE.fullmatch(tag)]
        dates += [parse_day(match[1]) for tag in tags if (match := NIGHTLY.fullmatch(tag))]
        if dates:
            day = max(dates)
            if day < today - datetime.timedelta(days=days):
                dated.append((day, version['name']))
    return {digest for _, digest in sorted(dated, reverse=True)[:limit]}


def descendants(roots, children):
    """Protect nested indexes and attestation/platform manifests transitively."""
    protected = set(roots)
    pending = list(roots)
    while pending:
        for child in children(pending.pop()):
            if child not in protected:
                protected.add(child)
                pending.append(child)
    return protected


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--replace-pr', type=int, help='Remove old versions of this PR before its new build')
    parser.add_argument('--only-untagged', action='store_true', help='Publication cleanup; leave all tagged versions for daily policy')
    args = parser.parse_args()
    repository = os.environ['GITHUB_REPOSITORY'].lower()
    owner, package = repository.split('/')
    kind = api(f'users/{owner}')['type']
    endpoint = f"{'orgs' if kind == 'Organization' else 'users'}/{owner}/packages/container/{package}/versions"
    pages = api(endpoint, '--paginate', '--slurp')
    versions = [version for page in pages for version in page]
    pr_cache, run_cache = {}, {}

    def open_pr(number):
        if number not in pr_cache:
            pr = api(f'repos/{repository}/pulls/{number}')
            pr_cache[number] = pr['state'] == 'open' and number != args.replace_pr
        return pr_cache[number]

    def active_run(number):
        if number not in run_cache:
            run_cache[number] = api(f'repos/{repository}/actions/runs/{number}')['status'] != 'completed'
        return run_cache[number]

    today = datetime.datetime.now(ZoneInfo('America/New_York')).date()

    recent_expired = recent_expired_versions(versions, today)
    reasons = {v['name']: retain(v['metadata']['container']['tags'], today, open_pr, active_run,
                                 recent_expired=v['name'] in recent_expired)
               for v in versions}

    def children(digest):
        # Any failed read aborts the whole plan, before the first DELETE.
        raw = subprocess.check_output(['docker', 'buildx', 'imagetools', 'inspect', '--raw',
                                       f'ghcr.io/{repository}@{digest}'], text=True)
        return [item['digest'] for item in json.loads(raw).get('manifests', [])]

    protected = descendants([digest for digest, reason in reasons.items() if reason], children)
    deletions = []
    for version in versions:
        tags = version['metadata']['container']['tags']
        if args.replace_pr is not None and not any(PR.fullmatch(t) and int(PR.fullmatch(t)[1]) == args.replace_pr for t in tags):
            continue
        if args.only_untagged and tags:
            continue
        keep = version['name'] in protected
        print(f"{'KEEP' if keep else 'DELETE'} {version['id']} {tags}: "
              f"{reasons[version['name']] or ('referenced manifest' if keep else 'unreferenced/expired')}")
        if not keep:
            deletions.append(version['id'])
    if args.apply:
        # Publishers and this job share application-images concurrency. Re-fetch
        # each version as an additional guard against tag movement outside Actions.
        for version_id in deletions:
            current = api(f'{endpoint}/{version_id}')
            if current['name'] in protected or retain(current['metadata']['container']['tags'], today, open_pr, active_run):
                raise RuntimeError('Retention changed during cleanup; refusing deletion')
            subprocess.run(['gh', 'api', '-X', 'DELETE', f'{endpoint}/{version_id}'], check=True)
