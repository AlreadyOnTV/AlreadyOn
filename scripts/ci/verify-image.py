#!/usr/bin/env python3
"""Require signed provenance and inventory for new cycles before promotion mutates tags."""
import argparse
import os
import re
import subprocess


def verify(repository, source, revision, run=subprocess.run):
    if not re.fullmatch(r'ghcr\.io/[a-z0-9_.-]+/[a-z0-9_.-]+@sha256:[0-9a-f]{64}', source):
        raise ValueError('Attested releases require an immutable image source')
    if not re.fullmatch(r'[0-9a-f]{40}', revision):
        raise ValueError('Expected a full source commit SHA')
    command = ['gh', 'attestation', 'verify', 'oci://' + source, '--repo', repository,
               '--signer-workflow', repository + '/.github/workflows/build_publish.yml',
               '--source-digest', revision, '--deny-self-hosted-runners']
    run(command, check=True)
    run([*command, '--predicate-type', 'https://cyclonedx.org/bom'], check=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', required=True)
    parser.add_argument('--revision', required=True)
    args = parser.parse_args()
    if not re.fullmatch(r'[0-9a-f]{40}', args.revision):
        parser.error('Expected a full source commit SHA')
    verify(os.environ['GITHUB_REPOSITORY'], args.source, args.revision)
