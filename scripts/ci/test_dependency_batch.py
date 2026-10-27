"""Exercise bot provenance, stale-head integration and failure boundaries offline."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('dependency_batch', Path(__file__).with_name('dependency-batch.py'))
batch = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(batch)
BASE, HEAD = 'a' * 40, 'b' * 40


def proposal():
    return {'number': 1, 'state': 'open', 'draft': False,
            'user': {'login': 'dependabot[bot]', 'type': 'Bot'}, 'base': {'ref': 'dev'},
            'head': {'sha': HEAD, 'ref': 'dependabot/npm/update', 'repo': {'full_name': 'owner/repo'}}}


class DependencyBatchTests(unittest.TestCase):
    def test_only_original_bot_same_repository_dev_proposals_are_eligible(self):
        self.assertTrue(batch.eligible(proposal(), 'Owner/Repo'))
        for field, value in [('state', 'closed'), ('draft', True), ('user', {'login': 'human', 'type': 'User'}),
                             ('base', {'ref': 'main'})]:
            pr = proposal()
            pr[field] = value
            self.assertFalse(batch.eligible(pr, 'owner/repo'))
        for field, value in [('repo', None), ('repo', {'full_name': 'fork/repo'}),
                             ('sha', 'latest'), ('ref', 'feature/update')]:
            pr = proposal()
            pr['head'][field] = value
            self.assertFalse(batch.eligible(pr, 'owner/repo'))

    def plan(self):
        return {'head': HEAD, 'base': BASE, 'branch': 'feature/batch', 'number': 2,
                'pulls': [{'number': 1, 'sha': HEAD}]}

    def test_concurrent_dev_or_batch_changes_prevent_any_write(self):
        for changed_pr in (True, False):
            pr = proposal()
            pr['head']['ref'] = 'feature/batch'
            if changed_pr:
                pr['head']['sha'] = 'c' * 40
            with patch.object(batch, 'api', side_effect=[pr, {'object': {'sha': 'c' * 40}}]) as api:
                with self.assertRaises(ValueError):
                    batch.integrate('owner/repo', self.plan())
                self.assertTrue(all(len(call.args) < 3 for call in api.call_args_list))

    def test_ref_rejection_prevents_dispatch_and_closure(self):
        pr = proposal()
        pr['head']['ref'] = 'feature/batch'
        with patch.object(batch, 'api', side_effect=[pr, {'object': {'sha': BASE}},
                         subprocess.CalledProcessError(1, ['gh'])]) as api:
            with self.assertRaises(subprocess.CalledProcessError):
                batch.integrate('owner/repo', self.plan())
            self.assertEqual(api.call_args_list[-1].args[3], {'sha': HEAD, 'force': False})
            self.assertEqual(api.call_count, 3)

    def test_dispatch_uses_exact_tested_sha_and_rebased_proposal_stays_open(self):
        pr = proposal()
        pr['head']['ref'] = 'feature/batch'
        rebased = proposal()
        rebased['head']['sha'] = 'c' * 40
        with patch.object(batch, 'api', side_effect=[pr, {'object': {'sha': BASE}}, {}, None, rebased]) as api:
            batch.integrate('owner/repo', self.plan())
            self.assertEqual(api.call_args_list[3].args[3]['inputs'], {'revision': HEAD, 'publication': 'nightly'})
            self.assertEqual(api.call_count, 5)

    def test_no_updates_does_not_create_branch_or_publish(self):
        with patch.object(batch, 'run', return_value='[[]]'), patch.object(batch, 'api') as api, \
                patch.object(batch, 'output') as output:
            batch.collect('owner/repo')
            api.assert_not_called()
            output.assert_called_once_with(changed='false')

    def test_merge_conflict_never_updates_dev(self):
        pr = proposal()
        with patch.object(batch, 'run', return_value=json.dumps([[pr]])), \
                patch.dict(os.environ, GITHUB_RUN_ID='1', GITHUB_RUN_ATTEMPT='1'), \
                patch.object(batch, 'api', side_effect=[{'object': {'sha': BASE}}, {},
                    subprocess.CalledProcessError(1, ['gh'])]) as api:
            with self.assertRaises(subprocess.CalledProcessError):
                batch.collect('owner/repo')
            self.assertFalse(any(call.args[1] == 'git/refs/heads/dev' for call in api.call_args_list))


if __name__ == '__main__':
    unittest.main()
