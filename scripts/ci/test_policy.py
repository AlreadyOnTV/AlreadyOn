"""Regression tests for publication boundaries and documentation failures."""
import tempfile
import datetime
import importlib.util
import pathlib
import os
import subprocess
import unittest
from unittest.mock import patch
import sys
import runpy
import io
from contextlib import redirect_stdout

sys.path.insert(0, str(pathlib.Path(__file__).parent))

SHA = 'a' * 40
OTHER = 'b' * 40


def load(name):
    """Load command-line helpers whose filenames use repository hyphen style."""
    spec = importlib.util.spec_from_file_location(name, pathlib.Path(__file__).with_name(name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


tags_module = load('image-tags')
tags = tags_module.image_tags
docs = load('check-documentation').check_changes


class PublicationPolicy(unittest.TestCase):
    """Exercise channel exclusions and independently named PR candidates."""

    def test_channels_and_lowercase(self):
        instant = datetime.datetime(2026, 10, 1, 12, tzinfo=datetime.timezone.utc)
        for branch, channel in [('dev', 'nightly'), ('test', 'latest'), ('main', 'stable')]:
            result = tags('AlreadyOnApp/AlreadyOn', branch, SHA, instant)
            self.assertEqual(result[0], 'ghcr.io/alreadyonapp/alreadyon:' + channel)
            self.assertEqual(len(result), 2 if branch == 'dev' else 1)
            if branch == 'dev':
                self.assertEqual(result[1], 'ghcr.io/alreadyonapp/alreadyon:2026-10-01')
        for branch in ['feature/a', 'fix/a', 'bugfix/a', 'release']:
            with self.assertRaises(KeyError):
                tags('owner/repo', branch, SHA, instant)

    def test_only_full_commit_ids_become_tags(self):
        for value in ['', 'abc123', SHA.upper(), SHA + 'x', 'latest', SHA[:-1] + ';']:
            with self.assertRaises(ValueError):
                tags('owner/repo', 'dev', value)
            with self.assertRaises(ValueError):
                tags_module.sha(value)

    def test_source_is_the_selected_commit_not_the_destination(self):
        environment = dict(os.environ, GITHUB_REPOSITORY='Owner/Repo', GITHUB_REF_NAME='main',
                           CI_SOURCE_BRANCH='dev', CI_SELECTED_SHA=SHA, CI_REVISION=OTHER, CI_PR_NUMBER='123')
        output = subprocess.check_output(['python3', str(pathlib.Path(__file__).with_name('image-tags.py'))],
                                         env=environment, text=True).splitlines()
        self.assertTrue(output[0].startswith('tags=ghcr.io/owner/repo:nightly ghcr.io/owner/repo:'))
        self.assertEqual(output[1], 'source=ghcr.io/owner/repo:pr-123')

    def test_full_e2e_is_required_for_dev_prs_not_test_main_pushes(self):
        root = pathlib.Path(__file__).resolve().parents[2]
        validation = (root / '.github/workflows/pull-request.yml').read_text()
        test_action = (root / '.github/actions/test-source/action.yml').read_text()
        self.assertIn('pull_request:\n    branches: [dev]', validation)
        self.assertIn('pull-base: \'false\'', validation)
        self.assertIn('make test-e2e', test_action)
        self.assertNotIn('branches: [build, dev, test, main]', validation)


class RetentionPolicy(unittest.TestCase):
    """Deletion boundaries are tested without accessing the real package."""

    def setUp(self):
        self.module = load('clean-images')
        self.today = datetime.date(2026, 10, 5)

    def keep(self, tags, open_pr=lambda _: False, active_run=lambda _: False):
        return self.module.retain(tags, self.today, open_pr, active_run)

    def test_channels_never_expire(self):
        for channel in ['nightly', 'latest', 'stable']:
            self.assertIsNotNone(self.keep([channel, '2020-01-01', 'sha-' + SHA]))

    def test_date_boundary_and_multiple_dates(self):
        for days, protected in [(89, True), (90, True), (91, False)]:
            day = (self.today - datetime.timedelta(days=days)).isoformat()
            self.assertEqual(self.keep([day]) is not None, protected)
        self.assertIsNotNone(self.keep(['2020-01-01', '2026-10-05']))

    def test_candidates_and_unknown_tags(self):
        tag = 'pr-123-' + SHA
        self.assertIsNotNone(self.keep([tag], lambda n: n == 123))
        self.assertIsNone(self.keep([tag]))
        self.assertIsNotNone(self.keep(['pr-123'], lambda n: n == 123))
        self.assertIsNone(self.keep(['pr-123']))
        self.assertIsNone(self.keep(['sha-' + SHA]))
        self.assertIsNone(self.keep([]))
        self.assertIsNotNone(self.keep(['base-runtime']))
        self.assertIsNotNone(self.keep(['unexpected']))
        self.assertIsNotNone(self.keep(['rebuild-456-1'], active_run=lambda _: True))
        self.assertIsNone(self.keep(['rebuild-456-1']))

    def test_manifest_graph(self):
        graph = {'index': ['platform', 'attestation'], 'platform': [],
                 'attestation': ['nested'], 'nested': ['index']}
        self.assertEqual(self.module.descendants(['index'], graph.__getitem__), set(graph))
        with self.assertRaises(KeyError):
            self.module.descendants(['unreadable'], graph.__getitem__)

    def test_digest_records_are_package_scoped(self):
        import image_registry
        digest = 'sha256:' + 'c' * 64
        record = {'context': image_registry.CONTEXT, 'state': 'success',
                  'target_url': 'https://ghcr.io/v2/owner/repo/manifests/' + digest}
        with patch.object(image_registry, 'api', return_value=[[record]]):
            self.assertEqual(image_registry.release_source('Owner/Repo', SHA), 'ghcr.io/owner/repo@' + digest)
            self.assertEqual(image_registry.release_source('other/repo', SHA), 'ghcr.io/other/repo:sha-' + SHA)

    def test_cleanup_dry_run_and_failed_manifest_never_delete(self):
        import image_registry
        versions = [{'id': 1, 'name': 'sha256:' + 'a' * 64,
                     'metadata': {'container': {'tags': ['nightly']}}},
                    {'id': 2, 'name': 'sha256:' + 'b' * 64,
                     'metadata': {'container': {'tags': []}}}]

        def response(path, *args):
            if path.startswith('users/') and '/packages/' not in path:
                return {'type': 'User'}
            return [versions]

        path = str(pathlib.Path(__file__).with_name('clean-images.py'))
        with patch.dict(os.environ, GITHUB_REPOSITORY='owner/repo'), \
                patch.object(image_registry, 'api', side_effect=response), \
                patch('subprocess.check_output', return_value='{}'), \
                patch('subprocess.run') as delete, \
                patch.object(sys, 'argv', [path]), redirect_stdout(io.StringIO()):
            runpy.run_path(path, run_name='__main__')
            delete.assert_not_called()
        with patch.dict(os.environ, GITHUB_REPOSITORY='owner/repo'), \
                patch.object(image_registry, 'api', side_effect=response), \
                patch('subprocess.check_output', side_effect=RuntimeError('registry unavailable')), \
                patch('subprocess.run') as delete, \
                patch.object(sys, 'argv', [path, '--apply']):
            with self.assertRaises(RuntimeError):
                runpy.run_path(path, run_name='__main__')
            delete.assert_not_called()

    def test_new_york_dates(self):
        for utc, day in [('2026-01-02T04:59:59+00:00', '2026-01-01'),
                         ('2026-01-02T05:00:00+00:00', '2026-01-02'),
                         ('2026-07-02T03:59:59+00:00', '2026-07-01'),
                         ('2026-07-02T04:00:00+00:00', '2026-07-02'),
                         ('2026-03-08T07:00:00+00:00', '2026-03-08'),
                         ('2026-11-01T06:00:00+00:00', '2026-11-01')]:
            self.assertTrue(tags('owner/repo', 'dev', SHA, datetime.datetime.fromisoformat(utc))[1].endswith(day))

    def test_dev_date_tag_uses_explicit_pr_release_date(self):
        instant = datetime.datetime(2026, 10, 11, 12, tzinfo=datetime.timezone.utc)
        result = tags('owner/repo', 'dev', SHA, instant, release_date='2026-10-10')
        self.assertEqual(result[1], 'ghcr.io/owner/repo:2026-10-10')
        with self.assertRaisesRegex(ValueError, 'valid YYYY-MM-DD'):
            tags('owner/repo', 'dev', SHA, instant, release_date='2026-02-30')

    def test_daily_tags_are_shared_and_replaceable(self):
        instant = datetime.datetime(2026, 10, 1, 12, tzinfo=datetime.timezone.utc)
        self.assertEqual(tags('o/r', 'dev', SHA, instant), tags('o/r', 'dev', SHA, instant.replace(hour=13)))
        self.assertEqual(tags('o/r', 'test', SHA, instant), ['ghcr.io/o/r:latest'])
        self.assertEqual(tags('o/r', 'main', SHA, instant), ['ghcr.io/o/r:stable'])

    def test_replace_only_selected_pr_and_keep_channel_images(self):
        import image_registry
        versions = [{'id': 1, 'name': 'sha256:' + 'a' * 64,
                     'metadata': {'container': {'tags': ['pr-123']}}},
                    {'id': 2, 'name': 'sha256:' + 'b' * 64,
                     'metadata': {'container': {'tags': ['pr-456']}}},
                    {'id': 3, 'name': 'sha256:' + 'c' * 64,
                     'metadata': {'container': {'tags': ['pr-123', 'nightly']}}}]

        def response(path, *args):
            if '/pulls/' in path:
                return {'state': 'open'}
            if path.endswith('/versions/1'):
                return versions[0]
            if '/packages/' in path:
                return [versions]
            return {'type': 'User'}

        path = str(pathlib.Path(__file__).with_name('clean-images.py'))
        with patch.dict(os.environ, GITHUB_REPOSITORY='owner/repo'), \
                patch.object(image_registry, 'api', side_effect=response), \
                patch('subprocess.check_output', return_value='{}'), \
                patch('subprocess.run') as delete, \
                patch.object(sys, 'argv', [path, '--apply', '--replace-pr', '123']), redirect_stdout(io.StringIO()):
            runpy.run_path(path, run_name='__main__')
            delete.assert_called_once_with(['gh', 'api', '-X', 'DELETE',
                                           'users/owner/packages/container/repo/versions/1'], check=True)

    def test_release_push_uses_fresh_local_image_without_metadata_rebuild(self):
        import image_registry
        path = str(pathlib.Path(__file__).with_name('publish-image.py'))
        digest = 'sha256:' + 'd' * 64
        with patch.dict(os.environ, GITHUB_REPOSITORY='owner/repo', CI_REVISION=SHA,
                        SOURCE_IMAGE='alreadyon-ci:candidate',
                        IMAGE_TAGS='ghcr.io/owner/repo:nightly ghcr.io/owner/repo:2026-10-05'), \
                patch.object(image_registry, 'record') as record, \
                patch('subprocess.check_output', return_value='Digest: ' + digest), \
                patch('subprocess.run') as docker, \
                patch.object(sys, 'argv', [path, '--local']):
            runpy.run_path(path, run_name='__main__')
            self.assertEqual([call.args[0][1] for call in docker.call_args_list],
                             ['tag', 'push', 'tag', 'push'])
            record.assert_called_once_with('owner/repo', SHA, digest)

    def test_cleanup_has_no_event_trigger_and_dev_build_runs_on_merges(self):
        root = pathlib.Path(__file__).resolve().parents[2]
        cleanup = (root / '.github/workflows/image-retention.yml').read_text()
        self.assertNotIn('pull_request_target:', cleanup)
        self.assertNotIn('workflow_run:', cleanup)
        build = (root / '.github/workflows/rebuild.yml').read_text()
        self.assertIn('workflow_dispatch:', build)
        self.assertNotIn('  schedule:', build)
        self.assertIn('  push:', build)
        self.assertIn('branches: [dev]', build)


class WorkBranchPolicy(unittest.TestCase):
    """Assistant issue branches can contain code, docs, or both."""

    def test_numbered_build_accepts_docs_and_code(self):
        policy = load('branch-policy')
        policy.validate_work_branch('feature/build-001', ['docs/PLAYBACK.md'])
        policy.validate_work_branch('feature/build-001', ['server/main.go', 'docs/PLAYBACK.md'])
        with self.assertRaises(ValueError):
            policy.validate_work_branch('feature/ordinary', ['docs/PLAYBACK.md'])

    def test_assistant_branches_accept_code_docs_and_mixed_changes(self):
        policy = load('branch-policy')
        for name in ['codex/session-cookie-isolation', 'copilot/channel-table-actions']:
            policy.validate_work_branch(name, ['server/internal/profile/profile.go'])
            policy.validate_work_branch(name, ['docs/PLAYBACK.md'])
            policy.validate_work_branch(name, ['server/internal/profile/profile.go', 'docs/PLAYBACK.md'])

    def test_human_branches_preserve_docs_only_separation(self):
        policy = load('branch-policy')
        with self.assertRaises(ValueError):
            policy.validate_work_branch('feature/docs-update', ['docs/PLAYBACK.md'])
        with self.assertRaises(ValueError):
            policy.validate_work_branch('docs/code-update', ['server/main.go'])
        policy.validate_work_branch('docs/update', ['docs/PLAYBACK.md'])

    def test_work_branch_names_remain_restricted(self):
        policy = load('branch-policy')
        for name in ['codex/', 'codex/-invalid', 'copilot/', 'copilot/-invalid', 'copilot/bad name', 'main',
                     'codex/one', 'codex/too-many-description-words', 'copilot/Upper-case',
                     'dev', 'test', 'promotion/dev-example', 'other/example']:
            with self.subTest(name=name), self.assertRaises(ValueError):
                policy.validate_work_branch(name, ['server/main.go'])
        for name in ['feature/example', 'bugfix/example', 'codex/two-words', 'copilot/fix-v2']:
            policy.validate_work_branch(name, ['server/main.go'])


class DocumentationPolicy(unittest.TestCase):
    """Check rejection as well as acceptance; deletions and odd paths matter."""

    def test_clean_checkout_checks_latest_commit_and_respects_explicit_base(self):
        script = pathlib.Path(__file__).with_name('check-documentation.py').resolve()
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            def git(*args):
                return subprocess.run(['git', *args], cwd=root, check=True,
                                      capture_output=True, text=True)
            git('init')
            git('config', 'user.name', 'Synthetic test')
            git('config', 'user.email', 'test@example.invalid')
            (root / 'README.md').write_text('Synthetic documentation.\n')
            git('add', '.')
            git('commit', '-m', 'Initial documentation')
            (root / 'entry.ts').write_text('export const value = 1;\n')
            git('add', '.')
            git('commit', '-m', 'Undocumented source')
            environment = dict(os.environ)
            environment.pop('CI_BASE', None)
            result = subprocess.run(['python3', str(script)], cwd=root, env=environment,
                                    capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('entry.ts: add an explanatory', result.stderr)
            environment['CI_BASE'] = 'HEAD'
            result = subprocess.run(['python3', str(script)], cwd=root, env=environment,
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_requires_markdown_and_comment(self):
        self.assertEqual(len(docs([('M', 'web/a.ts')], lambda _: 'export const x = 1;')), 2)
        changes = [('M', 'web/a.ts'), ('M', 'docs/API.md')]
        self.assertEqual(docs(changes, lambda _: '// Explain the public contract here.\nexport const x = 1;'), [])
        self.assertEqual(len(docs(changes, lambda _: '// TODO\nexport const x = 1;')), 1)

    def test_namespace_only_changes_do_not_require_source_comments(self):
        changes = [('M', 'server/a.go'), ('M', 'apps/android/Main.kt'), ('M', 'docs/API.md')]
        read_text = lambda _: 'package alreadyon\n'
        read_diff = lambda name: (['"old/module/pkg"', '"new/module/pkg"'] if name.endswith('.go')
                                  else ['package io.github.alreadyonapp.alreadyon',
                                        'package io.github.alreadyonapp.alreadyon'])
        self.assertEqual(docs(changes, read_text, read_diff), [])
        read_diff = lambda _: ['"old/module/pkg"', '"new/module/pkg"', 'func changed() {}']
        self.assertEqual(len(docs(changes[:1] + changes[2:], read_text, read_diff)), 1)

    def test_deleted_code_requires_docs_but_is_not_read(self):
        def unreadable(_):
            raise AssertionError('Deleted code must not be read')
        self.assertEqual(len(docs([('D', 'server/old.go')], unreadable)), 1)
        self.assertEqual(docs([('D', 'server/old.go'), ('M', 'README.md')], unreadable), [])
        self.assertEqual(len(docs([('D', 'server/old.go'), ('D', 'README.md')], unreadable)), 1)

    def test_infrastructure_and_spaced_paths(self):
        for name in ['deploy/Dockerfile', 'Makefile', '.github/workflows/a.yml', 'scripts/a b.py']:
            self.assertEqual(len(docs([('A', name)], lambda _: '# Explain this deployment or helper contract.')), 1)
        self.assertEqual(docs([('M', 'README.md')], lambda _: ''), [])


if __name__ == '__main__':
    unittest.main()
