#!/usr/bin/env python3
"""Combine same-repository Dependabot proposals; advance dev only to the tested SHA.

The write-token jobs run this trusted helper, never candidate scripts or actions.
A non-forced ref update is the atomic freshness check: if dev advances during
validation, integration fails rather than merging an untested combination.
"""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
from datetime import datetime
from zoneinfo import ZoneInfo


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def api(repo, path, method='GET', payload=None):
    command = ['gh', 'api', f'repos/{repo}/{path}', '--method', method]
    if payload is not None:
        command += ['--input', '-']
    result = subprocess.run(command, input=json.dumps(payload) if payload is not None else None,
                            text=True, capture_output=True, check=True)
    return json.loads(result.stdout) if result.stdout.strip() else None


def eligible(pr, repo):
    """Only authenticated bot PRs from this repository into dev are candidates."""
    return (pr['state'] == 'open' and not pr['draft'] and pr['user']['login'] == 'dependabot[bot]'
            and pr['user']['type'] == 'Bot' and pr['base']['ref'] == 'dev'
            and pr['head']['repo'] is not None and pr['head']['repo']['full_name'].lower() == repo.lower()
            and pr['head']['ref'].startswith('dependabot/')
            and re.fullmatch(r'[0-9a-f]{40}', pr['head']['sha']) is not None)


def output(**values):
    with open(os.environ['GITHUB_OUTPUT'], 'a') as stream:
        for key, value in values.items():
            stream.write(f'{key}={value}\n')


def collect(repo):
    pages = json.loads(run('gh', 'api', '--paginate', '--slurp',
                          f'repos/{repo}/pulls?state=open&base=dev&per_page=100'))
    prs = sorted((pr for page in pages for pr in page if eligible(pr, repo)), key=lambda pr: pr['number'])
    if not prs:
        output(changed='false')
        return
    base = api(repo, 'git/ref/heads/dev')['object']['sha']
    day = datetime.now(ZoneInfo('America/New_York')).strftime('%y.%m.%d')
    branch = f"feature/dependency-weekly-{day}-{os.environ['GITHUB_RUN_ID']}-{os.environ['GITHUB_RUN_ATTEMPT']}"
    api(repo, 'git/refs', 'POST', {'ref': 'refs/heads/' + branch, 'sha': base})
    head = base
    # Server-side merges never execute code from proposed dependency upgrades.
    # A conflict aborts the entire batch, retaining the individual PRs for repair.
    for pr in prs:
        merged = api(repo, 'merges', 'POST', {'base': branch, 'head': pr['head']['sha'],
                     'commit_message': f"Merge dependency update #{pr['number']} into weekly batch"})
        if merged:
            head = merged['sha']
    body = ('## Problem and resulting behavior\n\nIntegrate all open Dependabot updates, including major versions, '
            'as one tested change.\n\n' + '\n'.join(f"- {pr['html_url']}" for pr in prs)
            + '\n\n## Validation\n\nFull quality, Go/browser e2e, packaged-runtime, Android and security gates '
              'run before automatic integration. Failures leave dev unchanged. Nightly publication repeats '
              'the gates at the exact integrated source.\n\n## Risks and compatibility\n\n'
              'Major upgrades may require repairs. No alpha, RC, Stable or deployment is automatic.')
    pr = api(repo, 'pulls', 'POST', {'title': f'chore(deps): weekly dependency batch {day}',
                                   'base': 'dev', 'head': branch, 'body': body})
    api(repo, f"issues/{pr['number']}/labels", 'POST', {'labels': ['dependencies']})
    plan = {'base': base, 'head': head, 'branch': branch, 'number': pr['number'],
            'pulls': [{'number': p['number'], 'sha': p['head']['sha']} for p in prs]}
    output(changed='true', revision=head, plan=json.dumps(plan, separators=(',', ':')))
    with open(os.environ['GITHUB_STEP_SUMMARY'], 'a') as stream:
        stream.write(f"Combined {len(prs)} Dependabot PRs: {pr['html_url']}\n")


def integrate(repo, plan):
    head, base = plan['head'], plan['base']
    pr = api(repo, f"pulls/{int(plan['number'])}")
    if ((pr['state'] != 'open' and not pr.get('merged')) or pr['head']['sha'] != head or pr['base']['ref'] != 'dev'
            or pr['head']['ref'] != plan['branch']):
        raise ValueError('Batch PR changed during validation; create and test a new batch')
    current = api(repo, 'git/ref/heads/dev')['object']['sha']
    if current != base and current != head:
        raise ValueError('Dev advanced during validation; create and test a new batch')
    # force:false cannot overwrite a concurrent independent dev commit. GitHub
    # records the PR as merged when its exact head becomes reachable from dev.
    api(repo, 'git/refs/heads/dev', 'PATCH', {'sha': head, 'force': False})
    # GITHUB_TOKEN ref writes do not trigger push workflows; dispatch explicitly.
    api(repo, 'actions/workflows/rebuild.yml/dispatches', 'POST',
        {'ref': 'dev', 'inputs': {'revision': head, 'publication': 'nightly'}})
    for item in plan['pulls']:
        original = api(repo, f"pulls/{int(item['number'])}")
        # Do not close a rebased/newer proposal not included in this tested batch.
        if eligible(original, repo) and original['head']['sha'] == item['sha']:
            api(repo, f"pulls/{int(item['number'])}", 'PATCH', {'state': 'closed'})
    print(f'Integrated tested dependency batch {head}; full nightly validation dispatched')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('operation', choices=['collect', 'integrate'])
    args = parser.parse_args()
    repo = os.environ['GITHUB_REPOSITORY']
    if args.operation == 'collect':
        collect(repo)
    else:
        integrate(repo, json.loads(os.environ['DEPENDENCY_BATCH']))


if __name__ == '__main__':
    main()
