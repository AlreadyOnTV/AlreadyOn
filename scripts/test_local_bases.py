"""Verify local cache invalidation and parent pinning without building containers."""
import importlib.util
from datetime import datetime, timedelta, timezone
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('local_bases', Path(__file__).with_name('local-bases.py'))
bases = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bases)


def image(identity, recipe, parent=''):
    """Represent just the engine metadata used by cache selection."""
    return {'Id': identity, 'Created': datetime.now(timezone.utc).isoformat(), 'Config': {'Labels': {
        'io.alreadyon.base.recipe': recipe, 'io.alreadyon.base.runtime': parent}}}


class LocalBaseTests(unittest.TestCase):
    """A recipe match is insufficient when a refresh changed the runtime parent."""
    def test_published_pair_is_pulled_and_reused_without_building(self):
        published = {
            'ghcr.io/alreadyontv/alreadyon-base:base-runtime': image('remote-runtime', 'runtime-key'),
            'ghcr.io/alreadyontv/alreadyon-base:base-ci': image('remote-ci', 'ci-key', 'remote-runtime'),
        }
        for candidate in published.values():
            candidate['Created'] = (datetime.now(timezone.utc) - timedelta(days=8)).isoformat()
        available = {}
        def execute(command, **kwargs):
            if command[1] == 'pull':
                candidate = published[command[2]]
                available[command[2]] = candidate
                available[candidate['Id']] = candidate
            elif command[1] == 'tag':
                available[command[3]] = available[command[2]]
            else:
                self.fail('Compatible published bases must not be rebuilt')
            return subprocess.CompletedProcess(command, 0, '', '')
        with patch.object(bases, 'inspect', side_effect=lambda engine, reference: available.get(reference)), \
             patch.object(bases.keys, 'recipe_key', side_effect=lambda root, kind: kind + '-key'), \
             patch.object(bases.subprocess, 'run', side_effect=execute) as run:
            self.assertEqual(bases.ensure('podman'), {'runtime': 'remote-runtime', 'ci': 'remote-ci'})
            self.assertEqual([call.args[0][1] for call in run.call_args_list], ['pull', 'tag', 'pull', 'tag'])

    def test_registry_auth_failure_aborts_instead_of_building(self):
        failure = subprocess.CompletedProcess([], 1, '', 'unauthorized')
        with patch.object(bases, 'inspect', return_value=None), \
             patch.object(bases.keys, 'recipe_key', return_value='key'), \
             patch.object(bases.subprocess, 'run', return_value=failure) as run:
            with self.assertRaisesRegex(RuntimeError, 'unauthorized'):
                bases.ensure('podman')
            self.assertEqual(run.call_count, 1)

    def test_missing_published_manifest_permits_local_fallback(self):
        for reason in ('manifest unknown', 'not found', 'name unknown'):
            with self.subTest(reason=reason), \
                 patch.object(bases.subprocess, 'run', return_value=subprocess.CompletedProcess([], 1, '', reason)):
                self.assertIsNone(bases.pull('podman', 'published'))

    def test_registry_transport_failure_does_not_permit_local_fallback(self):
        for reason in ('connection timed out', 'TLS certificate verification failed', 'too many requests'):
            with self.subTest(reason=reason), \
                 patch.object(bases.subprocess, 'run', return_value=subprocess.CompletedProcess([], 1, '', reason)):
                with self.assertRaisesRegex(RuntimeError, reason):
                    bases.pull('podman', 'published')

    def test_age_boundary_and_native_creation_fallback(self):
        now = datetime(2026, 10, 9, tzinfo=timezone.utc)
        candidate = image('runtime', 'key')
        candidate['Created'] = (now - timedelta(days=7)).isoformat()
        self.assertFalse(bases.expired(candidate, now))
        candidate['Created'] = (now - timedelta(days=7, seconds=1)).isoformat()
        self.assertTrue(bases.expired(candidate, now))
        candidate['Config']['Labels']['io.alreadyon.base.built-at'] = now.isoformat()
        self.assertFalse(bases.expired(candidate, now))

    def test_invalid_or_future_timestamp_requires_refresh(self):
        now = datetime.now(timezone.utc)
        for stamp in ('', 'invalid', '2026-10-09T00:00:00',
                      (now + timedelta(days=1)).isoformat()):
            candidate = image('runtime', 'key')
            candidate['Created'] = stamp
            self.assertTrue(bases.expired(candidate, now))

    def test_expired_runtime_refreshes_both_bases(self):
        runtime = image('runtime-id', 'runtime-key')
        runtime['Created'] = (datetime.now(timezone.utc) - timedelta(days=8)).isoformat()
        ci = image('ci-id', 'ci-key', 'runtime-id')
        def lookup(engine, reference):
            return ci if reference.endswith('base-ci') else runtime
        with patch.object(bases, 'inspect', side_effect=lookup), \
             patch.object(bases, 'pull', return_value=None), \
             patch.object(bases.keys, 'recipe_key', side_effect=lambda root, kind: kind + '-key'), \
             patch.object(bases.subprocess, 'run') as run:
            bases.ensure('podman')
            self.assertEqual(run.call_count, 4)
            builds = [call.args[0] for call in run.call_args_list if call.args[0][1] == 'build']
            for command in builds:
                self.assertIn('--no-cache', command)
            self.assertIn('--pull=always', builds[0])
            self.assertIn('--pull=missing', builds[1])
            self.assertEqual([call.args[0][1] for call in run.call_args_list], ['build', 'pull', 'pull', 'build'])

    def test_expired_ci_keeps_fresh_runtime(self):
        runtime = image('runtime-id', 'runtime-key')
        ci = image('ci-id', 'ci-key', 'runtime-id')
        ci['Created'] = (datetime.now(timezone.utc) - timedelta(days=8)).isoformat()
        def lookup(engine, reference):
            return ci if reference.endswith('base-ci') else runtime
        with patch.object(bases, 'inspect', side_effect=lookup), \
             patch.object(bases, 'pull', return_value=None), \
             patch.object(bases.keys, 'recipe_key', side_effect=lambda root, kind: kind + '-key'), \
             patch.object(bases.subprocess, 'run') as run:
            bases.ensure('podman')
            self.assertEqual(run.call_count, 3)
            self.assertEqual([call.args[0][1] for call in run.call_args_list], ['pull', 'pull', 'build'])
            self.assertIn('--pull=missing', run.call_args.args[0])
            self.assertIn('ci-base', run.call_args.args[0])
            self.assertIn('--no-cache', run.call_args.args[0])

    def test_ci_parent_must_match_immutable_runtime(self):
        candidate = image('ci', 'key', 'upstream@sha256:digest')
        with patch.object(bases, 'inspect', return_value={'Id': 'runtime-old'}):
            self.assertFalse(bases.matches('podman', candidate, 'key', 'runtime-new'))
        with patch.object(bases, 'inspect', return_value={'Id': 'runtime-new'}):
            self.assertTrue(bases.matches('podman', candidate, 'key', 'runtime-new'))

    def test_engine_error_is_not_treated_as_missing_image(self):
        failure = subprocess.CompletedProcess([], 1, '', 'permission denied opening storage')
        with patch.object(bases.subprocess, 'run', return_value=failure):
            with self.assertRaises(RuntimeError):
                bases.inspect('podman', 'base')

    def test_missing_image_can_be_rebuilt(self):
        missing = subprocess.CompletedProcess([], 1, '', 'image not known')
        with patch.object(bases.subprocess, 'run', return_value=missing):
            self.assertIsNone(bases.inspect('podman', 'base'))

    def test_matching_local_fallback_does_not_build(self):
        runtime = image('runtime-id', 'runtime-key')
        ci = image('ci-id', 'ci-key', 'runtime-id')
        def lookup(engine, reference):
            return ci if reference.endswith('base-ci') else runtime
        with patch.object(bases, 'inspect', side_effect=lookup), \
             patch.object(bases, 'pull', return_value=None) as pull, \
             patch.object(bases.keys, 'recipe_key', side_effect=lambda root, kind: kind + '-key'), \
             patch.object(bases.subprocess, 'run') as run:
            self.assertEqual(bases.ensure('podman'), {'runtime': 'runtime-id', 'ci': 'ci-id'})
            run.assert_not_called()
            self.assertEqual(pull.call_count, 2)

    def test_forced_refresh_bypasses_cache_and_pins_ci_parent(self):
        runtime = image('runtime-id', 'runtime-key')
        ci = image('ci-id', 'ci-key', 'runtime-id')
        def lookup(engine, reference):
            return ci if reference.endswith('base-ci') else runtime
        with patch.object(bases, 'inspect', side_effect=lookup), \
             patch.object(bases.keys, 'recipe_key', side_effect=lambda root, kind: kind + '-key'), \
             patch.object(bases.subprocess, 'run') as run:
            bases.ensure('podman', refresh=True)
            self.assertEqual(run.call_count, 4)
            builds = [call.args[0] for call in run.call_args_list if call.args[0][1] == 'build']
            for command in builds:
                self.assertIn('--no-cache', command)
            self.assertIn('--pull=always', builds[0])
            self.assertIn('--pull=missing', builds[1])
            self.assertEqual([call.args[0][1] for call in run.call_args_list], ['build', 'pull', 'pull', 'build'])
            self.assertIn('RUNTIME_BASE=runtime-id', run.call_args_list[-1].args[0])


if __name__ == '__main__':
    unittest.main()
