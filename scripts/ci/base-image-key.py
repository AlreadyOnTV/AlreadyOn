#!/usr/bin/env python3
"""Fingerprint runtime and CI recipes independently, excluding application code."""
import argparse
import hashlib
import pathlib


def recipe_key(root, kind):
    """Frontend dependency updates invalidate CI tooling, not the media runtime."""
    inputs = (['deploy/base.Dockerfile', 'deploy/check-media-stack.sh',
               'deploy/patches/ffmpeg-readrate-initial-burst.patch'] if kind == 'runtime'
              else ['deploy/ci-base.Dockerfile', 'deploy/check-npm-tooling.js', 'deploy/npm-tooling/package.json',
                    'deploy/npm-tooling/package-lock.json', 'web/package.json', 'web/package-lock.json'])
    digest = hashlib.sha256()
    for name in inputs:
        digest.update(name.encode() + b'\0')
        digest.update((root / name).read_bytes())
        digest.update(b'\0')
    return digest.hexdigest()[:16]


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('kind', choices=['runtime', 'ci'], nargs='?', default='runtime')
    args = parser.parse_args()
    print(recipe_key(pathlib.Path(__file__).resolve().parents[2], args.kind))
