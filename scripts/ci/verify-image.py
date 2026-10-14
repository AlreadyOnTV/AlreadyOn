#!/usr/bin/env python3
"""Require signed provenance and inventory for new cycles before promotion mutates tags."""
import argparse
import os
import re
import subprocess
from datetime import date
from release_dates import parse_day


def verify(repository, source, revision, run=subprocess.run):
    # Earlier cycles predate attestations; do not manufacture provenance retroactively.
    day = subprocess.check_output(['git', 'show', f'{revision}:release-date.txt'], text=True).strip()
    # Historical source files are immutable; compare their canonical calendar date.
    if parse_day(day, historical=True) < date(2026, 10, 18):
        return
    if not re.fullmatch(r'ghcr\.io/[a-z0-9_.-]+/[a-z0-9_.-]+@sha256:[0-9a-f]{64}', source):
        raise ValueError('Attested releases require an immutable image source')
    command = ['gh', 'attestation', 'verify', 'oci://' + source, '--repo', repository,
               '--signer-workflow', repository + '/.github/workflows/rebuild.yml',
               '--deny-self-hosted-runners']
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
