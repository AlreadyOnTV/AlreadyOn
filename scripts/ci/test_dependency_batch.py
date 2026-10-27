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
        return {'head': HEAD, 'base': BASE, 'branch': 'feature/batch',
                'pulls': [{'number': 1, 'sha': HEAD}]}

    def test_concurrent_dev_or_batch_changes_prevent_any_write(self):
        for branch, dev in [('c' * 40, BASE), (HEAD, 'c' * 40)]:
            with patch.object(batch, 'api', side_effect=[{'object': {'sha': branch}},
                              {'object': {'sha': dev}}]) as api:
                with self.assertRaises(ValueError):
                    batch.integrate('owner/repo', self.plan())
                self.assertTrue(all(len(call.args) < 3 for call in api.call_args_list))

    def test_ref_rejection_prevents_dispatch_and_closure(self):
        pr = {'object': {'sha': HEAD}}
        with patch.object(batch, 'api', side_effect=[pr, {'object': {'sha': BASE}},
                         subprocess.CalledProcessError(1, ['gh'])]) as api:
            with self.assertRaises(subprocess.CalledProcessError):
                batch.integrate('owner/repo', self.plan())
            self.assertEqual(api.call_args_list[-1].args[3], {'sha': HEAD, 'force': False})
            self.assertEqual(api.call_count, 3)

    def test_dispatch_uses_exact_tested_sha_and_rebased_proposal_stays_open(self):
        pr = {'object': {'sha': HEAD}}
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


class InitialScheduleTests(unittest.TestCase):
    def setUp(self):
        from datetime import datetime, timezone
        self.now = datetime(2026, 10, 27, 12, tzinfo=timezone.utc)
        spec = importlib.util.spec_from_file_location('batch_health', Path(__file__).with_name('maintenance-health.py'))
        self.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.module)

    def test_activation_after_monday_waits_for_next_slot_across_dst(self):
        due = self.module.first_update_due('2026-10-26T23:00:00-04:00')
        self.assertEqual(due.isoformat(), '2026-11-02T06:30:00-05:00')
        self.assertIn('Awaiting first scheduled', self.module.health([], self.now, due))

    def test_manual_success_cannot_extend_first_schedule_deadline(self):
        due = self.module.first_update_due('2026-10-12T23:00:00-04:00')
        runs = [{'event': 'workflow_dispatch', 'conclusion': 'success', 'updated_at': self.now.isoformat()}]
        with self.assertRaises(ValueError):
            self.module.health(runs, self.now, due)

    def test_failed_first_schedule_does_not_receive_activation_grace(self):
        due = self.module.first_update_due('2026-10-26T23:00:00-04:00')
        runs = [{'event': 'schedule', 'conclusion': 'failure', 'updated_at': self.now.isoformat()}]
        with self.assertRaises(ValueError):
            self.module.health(runs, self.now, due)


if __name__ == '__main__':
    unittest.main()
