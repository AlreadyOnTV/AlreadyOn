#!/usr/bin/env python3
"""Publish tested layers, recording a digest rather than retaining commit tags."""
import argparse
import os
import subprocess
import re
from image_registry import record


def run(*args):
    subprocess.run(args, check=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--local', action='store_true', help='Push the newly built and runtime-tested local image')
    args = parser.parse_args()
    source = os.environ['SOURCE_IMAGE']
    tags = os.environ['IMAGE_TAGS'].split()
    revision = os.environ['CI_REVISION']
    if not re.fullmatch(r'[0-9a-f]{40}', revision):
        raise ValueError('Expected a full release commit SHA')
    if args.local:
        # The dev merge/scheduled build is independent of disposable PR images.
        for tag in tags:
            run('docker', 'tag', source, tag)
            run('docker', 'push', tag)
    else:
        run('docker', 'buildx', 'imagetools', 'create', '--prefer-index=false',
            *[arg for tag in tags for arg in ('-t', tag)], source)
    digests = []
    for tag in tags:
        output = subprocess.check_output(['docker', 'buildx', 'imagetools', 'inspect', tag], text=True)
        value = next(line.split()[1] for line in output.splitlines() if line.startswith('Digest:'))
        if not re.fullmatch(r'sha256:[0-9a-f]{64}', value):
            raise ValueError('Invalid published digest')
        digests.append(value)
    if len(set(digests)) != 1 or ('@sha256:' in source and digests[0] != source.rsplit('@', 1)[1]):
        raise ValueError('Published channel digests do not match the tested source')
    digest = digests[0]
    record(os.environ['GITHUB_REPOSITORY'], revision, digest)
    selected = os.environ.get('CI_SELECTED_SHA')
    if selected and selected != revision:
        record(os.environ['GITHUB_REPOSITORY'], selected, digest)
