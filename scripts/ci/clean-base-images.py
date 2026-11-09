#!/usr/bin/env python3
"""Plan cleanup of superseded base-image versions older than 30 days."""
import argparse
from datetime import datetime, timedelta, timezone
import json
import os
import re
import subprocess
from image_registry import api

CURRENT = {'base-ci', 'base-runtime'}
BASE_TAG = re.compile(r'base-(?:ci|runtime)(?:-[0-9a-f]{16})?')


def created_at(version):
    value = version.get('created_at')
    return datetime.fromisoformat(value.replace('Z', '+00:00')) if value else None


def recognized(tags):
    return all(BASE_TAG.fullmatch(tag) for tag in tags)


def deletable(version, cutoff, protected):
    timestamp = created_at(version)
    tags = version['metadata']['container']['tags']
    return (version['name'] not in protected and not CURRENT.intersection(tags)
            and recognized(tags) and timestamp is not None and timestamp < cutoff)


def descendants(roots, children):
    protected = set(roots)
    pending = list(roots)
    while pending:
        for child in children(pending.pop()):
            if child not in protected:
                protected.add(child)
                pending.append(child)
    return protected


def active_runs(repository, own_run):
    for status in ('queued', 'in_progress', 'waiting', 'pending', 'requested'):
        pages = api(f'repos/{repository}/actions/runs?status={status}&per_page=100',
                    '--paginate', '--slurp')
        if any(str(run['id']) != own_run for page in pages for run in page['workflow_runs']):
            return True
    return False


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--days', type=int, default=30)
    args = parser.parse_args()
    repository = os.environ['GITHUB_REPOSITORY'].lower()
    owner, package = repository.split('/')
    kind = api(f'users/{owner}')['type']
    endpoint = f"{'orgs' if kind == 'Organization' else 'users'}/{owner}/packages/container/{package}-base/versions"
    pages = api(endpoint, '--paginate', '--slurp')
    versions = [version for page in pages for version in page]
    tags = {tag for version in versions for tag in version['metadata']['container']['tags']}
    if not CURRENT.issubset(tags):
        raise RuntimeError('Both current base aliases must exist before cleanup')

    def children(digest):
        raw = subprocess.check_output(['docker', 'buildx', 'imagetools', 'inspect', '--raw',
                                       f'ghcr.io/{repository}-base@{digest}'], text=True)
        return [item['digest'] for item in json.loads(raw).get('manifests', [])]

    roots = [version['name'] for version in versions
             if CURRENT.intersection(version['metadata']['container']['tags']) or
             not recognized(version['metadata']['container']['tags'])]
    protected = descendants(roots, children)
    cutoff = datetime.now(timezone.utc) - timedelta(days=args.days)
    targets = [version for version in versions if deletable(version, cutoff, protected)]
    for version in versions:
        reason = ('current alias or descendant' if version['name'] in protected else
                  'unrecognized tag or timestamp' if not recognized(version['metadata']['container']['tags']) or created_at(version) is None else
                  'within retention window' if created_at(version) >= cutoff else
                  'older than retention window')
        print(f"{'DELETE' if version in targets else 'KEEP'} {version['id']} {version['metadata']['container']['tags']}: {reason}")

    if not args.apply:
        print('Dry run: no base images deleted')
        return
    if active_runs(repository, os.environ.get('GITHUB_RUN_ID', '')):
        print('Skip base cleanup: another Actions run may be using the shared bases')
        return
    for version in targets:
        current = api(f"{endpoint}/{version['id']}")
        if not deletable(current, cutoff, protected):
            raise RuntimeError('Base image retention changed during cleanup; refusing deletion')
        subprocess.run(['gh', 'api', '-X', 'DELETE', f"{endpoint}/{version['id']}"], check=True)


if __name__ == '__main__':
    main()
