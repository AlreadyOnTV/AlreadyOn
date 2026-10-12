"""Regression checks for reusable bases and fail-closed daily storage cleanup."""
import importlib.util
import io
import json
import os
import pathlib
import tempfile
import unittest
import subprocess
from contextlib import redirect_stdout
from unittest.mock import patch


def load(name):
    spec = importlib.util.spec_from_file_location(name, pathlib.Path(__file__).with_name(name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


storage = load('clean-build-storage')
resolver = load('resolve-base')
keys = load('base-image-key')


class BuildStoragePolicy(unittest.TestCase):
    """No tests access the live registry or delete a real cache."""

    def test_only_pull_merge_refs_are_eligible(self):
        self.assertEqual(storage.cache_pr('refs/pull/123/merge'), 123)
        for ref in ('refs/heads/dev', 'refs/heads/main', 'refs/pull/123/head',
                    'refs/pull/1/merge-extra', ''):
            self.assertIsNone(storage.cache_pr(ref))

    def test_base_roots_and_unknown_tags_are_protected(self):
        for tags in (['base-ci'], ['base-runtime'], ['custom'], ['base-ci-invalid']):
            self.assertTrue(storage.keep_base(tags))
        for tags in ([], ['base-ci-' + 'a' * 16], ['base-runtime-' + 'b' * 16]):
            self.assertFalse(storage.keep_base(tags))
        self.assertTrue(storage.keep_base(['base-ci', 'base-ci-' + 'a' * 16]))

    def test_runtime_and_ci_recipe_matching(self):
        labels = {'io.alreadyon.base.recipe': 'key', 'io.alreadyon.base.runtime': 'runtime@digest'}
        self.assertTrue(resolver.matches(labels, 'key', ''))
        self.assertTrue(resolver.matches(labels, 'key', 'runtime@digest'))
        self.assertFalse(resolver.matches(labels, 'changed', ''))
        self.assertFalse(resolver.matches(labels, 'key', 'runtime@new'))
        self.assertFalse(resolver.matches({}, 'key', ''))

    def test_resolver_pins_matching_manifest(self):
        root = pathlib.Path(__file__).resolve().parents[2]
        key = keys.recipe_key(root, 'runtime')
        data = {'Manifest': {'digest': 'sha256:' + 'a' * 64},
                'Image': {'config': {'Labels': {'io.alreadyon.base.recipe': key}}}}
        with tempfile.NamedTemporaryFile() as output, \
                patch.dict(os.environ, GITHUB_OUTPUT=output.name), \
                patch('sys.argv', ['resolve-base.py', 'ghcr.io/owner/repo-base', 'runtime']), \
                patch.object(resolver.subprocess, 'run', return_value=subprocess.CompletedProcess(
                    [], 0, json.dumps(data), '')):
            resolver.main()
            result = pathlib.Path(output.name).read_text()
        self.assertIn('needed=false', result)
        self.assertIn('base-runtime@sha256:' + 'a' * 64, result)

    def test_resolver_missing_manifest_bootstraps_but_auth_failure_aborts(self):
        with tempfile.NamedTemporaryFile() as output, \
                patch.dict(os.environ, GITHUB_OUTPUT=output.name), \
                patch('sys.argv', ['resolve-base.py', 'ghcr.io/owner/repo-base', 'runtime']):
            with patch.object(resolver.subprocess, 'run', return_value=subprocess.CompletedProcess(
                    [], 1, '', 'manifest unknown')):
                resolver.main()
                self.assertIn('needed=true', pathlib.Path(output.name).read_text())
            with patch.object(resolver.subprocess, 'run', return_value=subprocess.CompletedProcess(
                    [], 1, '', 'unauthorized')):
                with self.assertRaises(RuntimeError):
                    resolver.main()

    def test_independent_keys_exclude_application_sources(self):
        # Temporary synthetic inputs exercise fingerprint invalidation without
        # editing this checkout's dependencies.
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            names = ['deploy/base.Dockerfile', 'deploy/check-media-stack.sh',
                     'deploy/patches/ffmpeg-readrate-initial-burst.patch',
                     'deploy/ci-base.Dockerfile', 'deploy/npm-tooling/package.json',
                     'deploy/npm-tooling/package-lock.json', 'web/package.json', 'web/package-lock.json']
            for name in names:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(name)
            runtime, ci = keys.recipe_key(root, 'runtime'), keys.recipe_key(root, 'ci')
            (root / 'web/package-lock.json').write_text('browser update')
            self.assertEqual(runtime, keys.recipe_key(root, 'runtime'))
            self.assertNotEqual(ci, keys.recipe_key(root, 'ci'))
            ci = keys.recipe_key(root, 'ci')
            (root / 'deploy/npm-tooling/package-lock.json').write_text('npm security update')
            self.assertEqual(runtime, keys.recipe_key(root, 'runtime'))
            self.assertNotEqual(ci, keys.recipe_key(root, 'ci'))
            ci = keys.recipe_key(root, 'ci')
            (root / 'deploy/base.Dockerfile').write_text('media update')
            self.assertNotEqual(runtime, keys.recipe_key(root, 'runtime'))
            self.assertEqual(ci, keys.recipe_key(root, 'ci'))

    def test_active_runs_exclude_only_this_cleanup(self):
        with patch.object(storage, 'api', return_value=[{'workflow_runs': [{'id': 1}, {'id': 2}]}]):
            runs = storage.active_runs('owner/repo', '1')
        self.assertTrue(runs)
        self.assertTrue(all(run['id'] == 2 for run in runs))

    def test_active_run_defers_before_any_storage_read(self):
        with patch.dict(os.environ, GITHUB_REPOSITORY='owner/repo'), \
                patch('sys.argv', ['clean-build-storage.py', '--apply']), \
                patch.object(storage, 'active_runs', return_value=[{'id': 2}]), \
                patch.object(storage, 'api') as api, patch.object(storage.subprocess, 'run') as delete, \
                redirect_stdout(io.StringIO()):
            storage.main()
            api.assert_not_called()
            delete.assert_not_called()

    def fixture_api(self, path, *unused):
        if '/actions/caches?' in path:
            return [{'actions_caches': [
                {'id': 1, 'ref': 'refs/pull/1/merge', 'size_in_bytes': 100},
                {'id': 2, 'ref': 'refs/pull/2/merge', 'size_in_bytes': 100},
                {'id': 3, 'ref': 'refs/heads/dev', 'size_in_bytes': 100}]}]
        if path.endswith('/pulls/1'):
            return {'state': 'closed'}
        if path.endswith('/pulls/2'):
            return {'state': 'open'}
        if path == 'users/owner':
            return {'type': 'User'}
        if path.endswith('/versions'):
            return [[{'id': index, 'name': digest, 'metadata': {'container': {'tags': tags}}}
                     for index, digest, tags in [
                         (1, 'runtime', ['base-runtime']), (2, 'ci', ['base-ci']),
                         (3, 'old', ['base-ci-' + 'a' * 16]), (4, 'child', [])]]]
        raise AssertionError(path)

    def test_dry_run_preserves_manifest_descendants_and_never_deletes(self):
        def manifest(command, **unused):
            return json.dumps({'manifests': [{'digest': 'child'}] if command[-1].endswith('@ci') else []})
        output = io.StringIO()
        with patch.dict(os.environ, GITHUB_REPOSITORY='owner/repo'), \
                patch('sys.argv', ['clean-build-storage.py']), \
                patch.object(storage, 'active_runs', return_value=[]), \
                patch.object(storage, 'api', side_effect=self.fixture_api), \
                patch.object(storage.subprocess, 'check_output', side_effect=manifest), \
                patch.object(storage.subprocess, 'run') as delete, redirect_stdout(output):
            storage.main()
            delete.assert_not_called()
        self.assertIn('DELETE cache 1', output.getvalue())
        self.assertNotIn('DELETE cache 2', output.getvalue())
        self.assertNotIn('DELETE cache 3', output.getvalue())
        self.assertIn('DELETE base 3', output.getvalue())
        self.assertNotIn('DELETE base 4', output.getvalue())

    def test_missing_base_root_aborts_before_deleting(self):
        def api(path, *args):
            result = self.fixture_api(path, *args)
            if path.endswith('/versions'):
                result[0] = result[0][1:]
            return result
        with patch.dict(os.environ, GITHUB_REPOSITORY='owner/repo'), \
                patch('sys.argv', ['clean-build-storage.py', '--apply']), \
                patch.object(storage, 'active_runs', return_value=[]), \
                patch.object(storage, 'api', side_effect=api), \
                patch.object(storage.subprocess, 'run') as delete, redirect_stdout(io.StringIO()):
            with self.assertRaises(RuntimeError):
                storage.main()
            delete.assert_not_called()

    def test_apply_stops_if_another_run_starts(self):
        with patch.dict(os.environ, GITHUB_REPOSITORY='owner/repo'), \
                patch('sys.argv', ['clean-build-storage.py', '--apply']), \
                patch.object(storage, 'active_runs', side_effect=[[], [{'id': 2}]]), \
                patch.object(storage, 'api', side_effect=self.fixture_api), \
                patch.object(storage.subprocess, 'check_output', return_value='{}'), \
                patch.object(storage.subprocess, 'run') as delete, redirect_stdout(io.StringIO()):
            storage.main()
            delete.assert_not_called()

    def test_apply_refuses_reopened_pr(self):
        calls = 0

        def api(path, *args):
            nonlocal calls
            if path.endswith('/pulls/1'):
                calls += 1
                return {'state': 'closed' if calls == 1 else 'open'}
            return self.fixture_api(path, *args)

        with patch.dict(os.environ, GITHUB_REPOSITORY='owner/repo'), \
                patch('sys.argv', ['clean-build-storage.py', '--apply']), \
                patch.object(storage, 'active_runs', return_value=[]), \
                patch.object(storage, 'api', side_effect=api), \
                patch.object(storage.subprocess, 'check_output', return_value='{}'), \
                patch.object(storage.subprocess, 'run') as delete, redirect_stdout(io.StringIO()):
            with self.assertRaises(RuntimeError):
                storage.main()
            delete.assert_not_called()


if __name__ == '__main__':
    unittest.main()
