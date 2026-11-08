"""Durable commit-to-digest records replace permanent sha-* registry tags."""
import json
import re
import subprocess

CONTEXT = 'alreadyon/release-image'
DIGEST = re.compile(r'sha256:[0-9a-f]{64}')


def api(path, *args):
    """Use the runner's scoped GH_TOKEN, never a token embedded in command arguments."""
    return json.loads(subprocess.check_output(['gh', 'api', path, *args], text=True))


def release_source(repository, revision):
    """Accept only digest records for this package; old releases still support sha tags."""
    prefix = f'https://ghcr.io/v2/{repository.lower()}/manifests/'
    pages = api(f'repos/{repository}/commits/{revision}/statuses', '--paginate', '--slurp')
    for status in (item for page in pages for item in page):
        target = status.get('target_url') or ''
        if status['context'] == CONTEXT and status['state'] == 'success' and target.startswith(prefix):
            digest = target[len(prefix):]
            if DIGEST.fullmatch(digest):
                return f'ghcr.io/{repository.lower()}@{digest}'
    return f'ghcr.io/{repository.lower()}:sha-{revision}'


def record(repository, revision, digest):
    """Promotions record the destination commit too, allowing test-to-main lookup."""
    if not DIGEST.fullmatch(digest):
        raise ValueError('Invalid image digest')
    api(f'repos/{repository}/statuses/{revision}', '-X', 'POST',
        '-f', 'state=success', '-f', f'context={CONTEXT}',
        '-f', f'target_url=https://ghcr.io/v2/{repository.lower()}/manifests/{digest}',
        '-f', 'description=Tested release image (subject to retention)')
