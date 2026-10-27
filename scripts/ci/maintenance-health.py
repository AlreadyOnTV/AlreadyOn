#!/usr/bin/env python3
"""Fail visibly when the weekly security review has no recent successful scheduled run."""
import argparse
from datetime import datetime, time, timedelta, timezone
from zoneinfo import ZoneInfo
import json
import os
import subprocess


def first_update_due(created_at):
    """First Monday 06:30 after activation, using New York's DST-aware calendar."""
    created = datetime.fromisoformat(created_at.replace('Z', '+00:00')).astimezone(ZoneInfo('America/New_York'))
    day = created.date() + timedelta(days=(-created.weekday()) % 7)
    due = datetime.combine(day, time(6, 30), tzinfo=created.tzinfo)
    return due if due > created else datetime.combine(day + timedelta(days=7), time(6, 30), tzinfo=created.tzinfo)


def health(runs, now, first_due=None):
    # Manual runs do not conceal a disabled/missed schedule. Future clocks fail closed.
    successful = [datetime.fromisoformat(run['updated_at'].replace('Z', '+00:00'))
                  for run in runs if run.get('event') == 'schedule' and run.get('conclusion') == 'success']
    if not successful:
        # Activation after this week's slot is not a missed run. This one-time
        # deadline comes from immutable workflow creation time, never manual runs.
        failed = any(run.get('event') == 'schedule' and run.get('conclusion') not in (None, '', 'success') for run in runs)
        if first_due is not None and now < first_due + timedelta(hours=24) and not failed:
            return 'Awaiting first scheduled dependency update: ' + first_due.isoformat()
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
    first_due = None
    if not args.runs and args.workflow == 'dependency-updates.yml':
        workflow = json.loads(subprocess.check_output(['gh', 'api',
            f'repos/{os.environ["GITHUB_REPOSITORY"]}/actions/workflows/{args.workflow}'], text=True))
        created = datetime.fromisoformat(workflow['created_at'].replace('Z', '+00:00'))
        if created > datetime.now(timezone.utc):
            raise ValueError('Workflow creation timestamp is in the future')
        first_due = first_update_due(workflow['created_at'])
    print('Weekly maintenance health:', health(response['workflow_runs'], datetime.now(timezone.utc), first_due))
