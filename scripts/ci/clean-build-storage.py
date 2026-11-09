#!/usr/bin/env python3
"""Daily closed-PR cache and superseded base cleanup; dry run unless --apply."""
import argparse
import json
import os
import re
import subprocess
from image_registry import api
from importlib.util import module_from_spec, spec_from_file_location
from pathlib import Path

PR_REF = re.compile(r'refs/pull/(\d+)/merge')
BASE_TAG = re.compile(r'base-(?:ci|runtime)(?:-[0-9a-f]{16})?')
CURRENT = {'base-ci', 'base-runtime'}


def cache_pr(ref):
    """Never delete normal branch caches or unfamiliar ref formats."""
    match = PR_REF.fullmatch(ref)
    return int(match[1]) if match else None


def keep_base(tags):
    """Protect current roots and unknown tags; remove only known obsolete aliases."""
    return bool(CURRENT.intersection(tags) or any(not BASE_TAG.fullmatch(t) for t in tags))


def active_runs(repository, own_run):
    """Fail closed on API errors; include waiting/queued runs as base consumers."""
    active = []
    for status in ('queued', 'in_progress', 'waiting', 'pending', 'requested'):
        pages = api(f'repos/{repository}/actions/runs?status={status}&per_page=100',
                    '--paginate', '--slurp')
        active.extend(run for page in pages for run in page['workflow_runs']
                      if str(run['id']) != own_run)
    return active


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--caches-only', action='store_true', help='Leave shared base images to their 30-day policy')
    args = parser.parse_args()
    repository = os.environ['GITHUB_REPOSITORY'].lower()
    own_run = os.environ.get('GITHUB_RUN_ID', '')
    # This conservative lease protects pinned images and rerun PR cache consumers.
    # Daily cleanup can retry tomorrow if today's release is still using them.
    if active_runs(repository, own_run):
        print('SKIP build storage: another Actions run may hold base/cache references')
        return
    pages = api(f'repos/{repository}/actions/caches?per_page=100', '--paginate', '--slurp')
    caches = [cache for page in pages for cache in page['actions_caches']]
    closed = {}
    cache_deletions = []
    for cache in caches:
        number = cache_pr(cache['ref'])
        if number is None:
            continue
        if number not in closed:
            closed[number] = api(f'repos/{repository}/pulls/{number}')['state'] == 'closed'
        if closed[number]:
            cache_deletions.append((cache['id'], number))
            print(f"DELETE cache {cache['id']} PR #{number}: {cache['size_in_bytes']} bytes")

    if args.caches_only:
        if not args.apply:
            print('Dry run: no caches deleted')
            return
        for cache_id, number in cache_deletions:
            if active_runs(repository, own_run):
                print('STOP cleanup: another Actions run started')
                return
            if api(f'repos/{repository}/pulls/{number}')['state'] != 'closed':
                raise RuntimeError('PR reopened during cleanup; refusing deletion')
            subprocess.run(['gh', 'api', '-X', 'DELETE',
                            f'repos/{repository}/actions/caches/{cache_id}'], check=True)
        return

    owner, name = repository.split('/')
    kind = api(f'users/{owner}')['type']
    endpoint = f"{'orgs' if kind == 'Organization' else 'users'}/{owner}/packages/container/{name}-base/versions"
    pages = api(endpoint, '--paginate', '--slurp')
    versions = [version for page in pages for version in page]
    present = {tag for version in versions for tag in version['metadata']['container']['tags']}
    if not CURRENT.issubset(present):
        raise RuntimeError('Both current base tags must exist before any build-storage deletion')
    spec = spec_from_file_location('retention', Path(__file__).with_name('clean-images.py'))
    retention = module_from_spec(spec)
    spec.loader.exec_module(retention)

    def children(digest):
        raw = subprocess.check_output(['docker', 'buildx', 'imagetools', 'inspect', '--raw',
                                       f'ghcr.io/{repository}-base@{digest}'], text=True)
        return [item['digest'] for item in json.loads(raw).get('manifests', [])]

    roots = [v['name'] for v in versions if keep_base(v['metadata']['container']['tags'])]
    protected = retention.descendants(roots, children)
    bases = [v for v in versions if v['name'] not in protected]
    for version in bases:
        print(f"DELETE base {version['id']}: {version['metadata']['container']['tags']}")
    if not args.apply:
        print('Dry run: no caches or images deleted')
        return
    # The cleanup job holds base-images and application-images concurrency locks.
    # Recheck external runs and PR/tag state before every irreversible operation.
    for cache_id, number in cache_deletions:
        if active_runs(repository, own_run):
            print('STOP cleanup: another Actions run started')
            return
        if api(f'repos/{repository}/pulls/{number}')['state'] != 'closed':
            raise RuntimeError('PR reopened during cleanup; refusing deletion')
        subprocess.run(['gh', 'api', '-X', 'DELETE',
                        f'repos/{repository}/actions/caches/{cache_id}'], check=True)
    for version in bases:
        if active_runs(repository, own_run):
            print('STOP cleanup: another Actions run started')
            return
        current = api(f"{endpoint}/{version['id']}")
        if keep_base(current['metadata']['container']['tags']) or current['name'] in protected:
            raise RuntimeError('Base tags changed during cleanup; refusing deletion')
        subprocess.run(['gh', 'api', '-X', 'DELETE', f"{endpoint}/{version['id']}"], check=True)


if __name__ == '__main__':
    main()
