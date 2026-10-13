#!/usr/bin/env python3
"""Upload tested layers without moving channels; attest the resulting immutable digest."""
import os
import re
import subprocess


def stage(repository, image, run_id, attempt):
    if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository):
        raise ValueError('Invalid repository')
    if not run_id.isdigit() or not attempt.isdigit():
        raise ValueError('Invalid workflow identity')
    name = f'ghcr.io/{repository.lower()}'
    tag = f'{name}:rebuild-{run_id}-{attempt}'
    subprocess.run(['docker', 'tag', image, tag], check=True)
    subprocess.run(['docker', 'push', tag], check=True)
    output = subprocess.check_output(['docker', 'buildx', 'imagetools', 'inspect', tag], text=True)
    digest = next((line.split()[1] for line in output.splitlines() if line.startswith('Digest:')), '')
    if not re.fullmatch(r'sha256:[0-9a-f]{64}', digest):
        raise ValueError('Registry returned no valid digest')
    return {'name': name, 'digest': digest, 'source': name + '@' + digest}


if __name__ == '__main__':
    result = stage(os.environ['GITHUB_REPOSITORY'], os.environ['SOURCE_IMAGE'],
                   os.environ['GITHUB_RUN_ID'], os.environ['GITHUB_RUN_ATTEMPT'])
    with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
        for key, value in result.items():
            output.write(f'{key}={value}\n')
