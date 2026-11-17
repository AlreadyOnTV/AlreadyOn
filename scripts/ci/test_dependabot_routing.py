"""Verify privileged routing ignores forks, human PRs, and changed eligibility."""
from pathlib import Path
import subprocess
import unittest
import yaml

ROOT = Path(__file__).resolve().parents[2]


class DependabotRouting(unittest.TestCase):
    def workflow(self):
        return yaml.safe_load((ROOT / '.github/workflows/dependabot_routing.yml').read_text())

    def test_privileged_workflow_never_loads_pr_code(self):
        workflow = self.workflow()
        self.assertEqual(workflow['permissions'], {})
        self.assertEqual(set(workflow[True]), {'workflow_run', 'schedule', 'workflow_dispatch'})
        steps = workflow['jobs']['route']['steps']
        self.assertEqual(len(steps), 2)
        token = steps[0]['with']
        self.assertEqual(token['permission-pull-requests'], 'write')
        self.assertNotIn('owner', token)
        self.assertNotIn('repositories', token)
        self.assertNotIn('skip-token-revoke', token)
        self.assertIn('secrets.DEPENDABOT_ROUTER_PRIVATE_KEY', token['private-key'])
        self.assertFalse(any(step.get('uses', '').startswith('actions/checkout@') for step in steps))

    def test_only_fresh_same_repository_bot_prs_are_retargeted(self):
        script = self.workflow()['jobs']['route']['steps'][1]['with']['script']
        harness = r'''
const assert = require('node:assert/strict');
const context = {repo: {owner: 'owner', repo: 'repo'}};
const core = {info() {}};
const pr = number => ({number, state: 'open', user: {login: 'dependabot[bot]', type: 'Bot'},
  base: {ref: 'main', repo: {full_name: 'owner/repo'}},
  head: {ref: 'dependabot/npm/security', repo: {full_name: 'owner/repo'}}});
const listed = Array.from({length: 10}, (_, index) => pr(index + 1));
listed[1].user.login = 'human';
listed[2].head.repo.full_name = 'fork/repo';
listed[3].head.ref = 'feature';
listed[4].base.ref = 'dev';
listed[5].state = 'closed';
listed[6].user.type = 'User';
listed[7].head.repo = null;
listed[8].base.repo.full_name = 'other/repo';
const reads = [], updates = [];
const github = {rest: {pulls: {list() {},
  async get({pull_number}) {
    reads.push(pull_number);
    const current = pr(pull_number);
    if (pull_number === 10) current.state = 'closed';
    return {data: current};
  },
  async update(change) {updates.push(change);}
}}, async paginate(method, query) {
  assert.equal(query.state, 'open'); assert.equal(query.base, 'main'); return listed;
}};
(async () => {
'''
        tail = r'''
assert.deepEqual(reads, [1, 10]);
assert.deepEqual(updates, [{owner: 'owner', repo: 'repo', pull_number: 1, base: 'dev'}]);
})().catch(error => {console.error(error); process.exitCode = 1;});
'''
        result = subprocess.run(['node', '-e', harness + script + tail], text=True,
                                capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
