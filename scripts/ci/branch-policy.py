#!/usr/bin/env python3
"""Enforce work-branch names, dev-containing PR branches and exact, ancestry-preserving promotion snapshots."""
import argparse
import base64
from datetime import datetime
import json
import os
from pathlib import Path
import re
import subprocess
import urllib.request


def git(*args):
    """Read repository state without interpolating revision names into a shell."""
    return subprocess.check_output(['git', *args], text=True).strip()


def ancestor(older, newer):
    """Fail closed if history cannot establish the promotion relationship."""
    return subprocess.run(['git', 'merge-base', '--is-ancestor', older, newer],
                          check=False).returncode == 0


def remote_head(branch):
    """Resolve current trusted refs without persisting checkout credentials."""
    url = f"https://api.github.com/repos/{os.environ['GITHUB_REPOSITORY']}/git/ref/heads/{branch}"
    request = urllib.request.Request(url, headers={'Authorization': 'Bearer ' + os.environ['GH_TOKEN']})
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)['object']['sha']


def fetch(ref):
    """Fetch an advanced trusted ref read-only; checkout credentials are not persisted."""
    auth = base64.b64encode(('x-access-token:' + os.environ['GH_TOKEN']).encode()).decode()
    environment = dict(os.environ, GIT_CONFIG_COUNT='1',
                       GIT_CONFIG_KEY_0='http.https://github.com/.extraheader',
                       GIT_CONFIG_VALUE_0='AUTHORIZATION: basic ' + auth)
    subprocess.run(['git', 'fetch', '--quiet', 'origin', ref], env=environment, check=True)


def validate_up_to_date(selected, dev_head):
    """A dev PR must contain dev so the tested tree is exactly the tree that merges."""
    if not ancestor(dev_head, selected):
        raise ValueError('Branch does not contain the current dev head; merge origin/dev and push again')


def emit(selected):
    """Publish the exact source commit so later workflow steps retag its image."""
    if os.environ.get('GITHUB_OUTPUT'):
        with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
            output.write(f'selected={selected}\n')


def validate_promotion(target, selected, upstream, result):
    """A promotion must retain source ancestry and its exact tree, including main."""
    if not ancestor(selected, upstream):
        raise ValueError('Selected commit is not in the source branch history')
    if target == 'main' and selected != upstream:
        raise ValueError('Main must promote the current test head')
    if git('rev-parse', selected + '^{tree}') != git('rev-parse', result + '^{tree}'):
        raise ValueError('Promotion merge changes the selected snapshot; resolve changes on dev first')


def promotion_source(result, upstream):
    """Accept an exact upstream fast-forward for an authorized baseline sync."""
    if result == upstream:
        return result
    parents = git('rev-list', '--parents', '-n', '1', result).split()[1:]
    if len(parents) != 2:
        raise ValueError('Release promotion requires a merge commit or the exact upstream head')
    return parents[1]


def validate_work_branch(name, files):
    """Accept isolated assistant branches while keeping human docs branches separate."""
    release_branch = re.fullmatch(r'\d{4}-\d{2}-\d{2}', name)
    if release_branch:
        try:
            datetime.strptime(name, '%Y-%m-%d')
        except ValueError as error:
            raise ValueError('Release branches must use a valid YYYY-MM-DD date') from error
    elif not re.fullmatch(r'(feature|bugfix|docs|codex|copilot)/[A-Za-z0-9][A-Za-z0-9._/-]*', name):
        raise ValueError('Issue branches must use YYYY-MM-DD, feature/, bugfix/, docs/, codex/ or copilot/')
    assistant = name.startswith(('codex/', 'copilot/'))
    if assistant and not re.fullmatch(r'(codex|copilot)/[a-z0-9]+(?:-[a-z0-9]+){1,2}', name):
        raise ValueError('Assistant branch descriptions must contain two or three lowercase hyphenated words')
    docs_only = bool(files) and all(Path(p).suffix.lower() in {'.md', '.rst', '.txt'}
                                   or (p.startswith('docs/') and Path(p).suffix.lower() in
                                       {'.png', '.jpg', '.jpeg', '.svg', '.webp'}) for p in files)
    legacy_build = bool(re.fullmatch(r'feature/build-[0-9]+', name))
    if docs_only and not (release_branch or name.startswith('docs/') or assistant or legacy_build):
        raise ValueError('Use YYYY-MM-DD, docs/, codex/ or copilot/ for documentation-only changes')
    if not docs_only and name.startswith('docs/'):
        raise ValueError('Use feature/, bugfix/, codex/ or copilot/ for code changes')


def main():
    """Validate a PR or a merged promotion, then recheck freshness before delivery."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--delivery', action='store_true')
    args = parser.parse_args()
    event = json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text())
    kind = os.environ['GITHUB_EVENT_NAME']
    branch = os.environ['GITHUB_REF_NAME']
    result = git('rev-parse', 'HEAD')
    if kind == 'pull_request':
        pr = event['pull_request']
        branch = pr['base']['ref']
        # The PR queue may have merged the current dev head into the branch first.
        selected = os.environ.get('CI_PR_HEAD_SHA') or pr['head']['sha']
        if branch == 'dev':
            files = git('diff', '--name-only', pr['base']['sha'] + '...' + selected).splitlines()
            validate_work_branch(pr['head']['ref'], files)
            dev_head = remote_head('dev')
            fetch(dev_head)
            validate_up_to_date(selected, dev_head)
            emit(selected)
            print('Branch policy passed')
            return
        if pr['head']['repo']['full_name'] != os.environ['GITHUB_REPOSITORY']:
            raise ValueError('Promotions must originate in this repository')
        if not pr['head']['ref'].startswith('promotion/' + branch + '-'):
            raise ValueError('Promotions use the approved fast-forward workflow, not a pull request')
    if branch in {'test', 'main'}:
        upstream = remote_head('dev' if branch == 'test' else 'test')
        if kind != 'pull_request':
            selected = promotion_source(result, upstream)
        # checkout fetch-depth:0 contains current branch history. Fetch newly advanced refs read-only.
        fetch(upstream)
        validate_promotion(branch, selected, upstream, result)
        emit(selected)
    if args.delivery and remote_head(branch) != result:
        raise ValueError('Branch advanced; obsolete candidate must not replace its channel/deployment')
    print('Branch and promotion policy passed')


if __name__ == '__main__':
    main()
