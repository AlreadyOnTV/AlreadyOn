#!/usr/bin/env python3
"""Fail visibly when the weekly security review has no recent successful scheduled run."""
import argparse
from datetime import datetime, timedelta, timezone
import json
import os
import subprocess


def health(runs, now):
    # Manual runs do not conceal a disabled/missed schedule. Future clocks fail closed.
    successful = [datetime.fromisoformat(run['updated_at'].replace('Z', '+00:00'))
                  for run in runs if run.get('event') == 'schedule' and run.get('conclusion') == 'success']
    if not successful:
        raise ValueError('No successful scheduled weekly dependency review exists')
    latest = max(successful)
    if latest > now or now - latest > timedelta(days=8):
        raise ValueError('Weekly dependency review is overdue (maximum age: eight days)')
    return latest.isoformat()


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--runs', type=argparse.FileType('r'), help='Synthetic/offline GitHub run response')
    parser.add_argument('--workflow', choices=['dependency-maintenance.yml', 'dependency-updates.yml'],
                        default='dependency-maintenance.yml')
    args = parser.parse_args()
    if args.runs:
        response = json.load(args.runs)
    else:
        response = json.loads(subprocess.check_output(['gh', 'api',
            f'repos/{os.environ["GITHUB_REPOSITORY"]}/actions/workflows/{args.workflow}/runs?event=schedule&per_page=100'], text=True))
    print('Latest successful scheduled review:', health(response['workflow_runs'], datetime.now(timezone.utc)))
