"""Regression tests for publication boundaries and documentation failures."""
import tempfile
import datetime
import importlib.util
import json
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
        expected = ['ghcr.io/alreadyontv/alreadyon:nightly',
                    'ghcr.io/alreadyontv/alreadyon:26.10.01',
                    'ghcr.io/alreadyontv/alreadyon:26.10.01-nightly']
        result = tags('AlreadyOnTV/AlreadyOn', 'main', SHA, instant)
        self.assertEqual(result, expected)
        self.assertEqual(tags('AlreadyOnTV/AlreadyOn', 'dev', SHA, instant), expected)
        self.assertEqual(tags('AlreadyOnTV/AlreadyOn', 'main', SHA, instant,
                              publication='latest'), ['ghcr.io/alreadyontv/alreadyon:latest'])
        for branch in ['feature/a', 'fix/a', 'bugfix/a', 'release', 'test']:
            with self.assertRaises(ValueError):
                tags('owner/repo', branch, SHA, instant)

    def test_only_full_commit_ids_become_tags(self):
        for value in ['', 'abc123', SHA.upper(), SHA + 'x', 'latest', SHA[:-1] + ';']:
            with self.assertRaises(ValueError):
                tags('owner/repo', 'dev', value)
            with self.assertRaises(ValueError):
                tags_module.sha(value)

    def test_source_is_the_selected_commit_not_the_destination(self):
        environment = dict(os.environ, GITHUB_REPOSITORY='Owner/Repo', GITHUB_REF_NAME='main',
                   CI_SOURCE_BRANCH='main', CI_SELECTED_SHA=SHA, CI_REVISION=OTHER, CI_PR_NUMBER='123')
        output = subprocess.check_output(['python3', str(pathlib.Path(__file__).with_name('image-tags.py'))],
                                         env=environment, text=True).splitlines()
        self.assertTrue(output[0].startswith('tags=ghcr.io/owner/repo:nightly ghcr.io/owner/repo:'))
        self.assertEqual(output[1], 'source=ghcr.io/owner/repo:pr-123')

    def test_dev_and_main_prs_require_full_e2e_codeql_and_dependency_review(self):
        root = pathlib.Path(__file__).resolve().parents[2]
        validation = (root / '.github/workflows/pull_request_validation.yml').read_text()
        codeql = (root / '.github/workflows/codeql.yml').read_text()
        build = (root / '.github/workflows/build_publish.yml').read_text()
        test_action = (root / '.github/actions/test-source/action.yml').read_text()
        self.assertIn('pull_request:\n    branches: [dev, main]', validation)
        self.assertIn('pull_request:\n    branches: [dev, main]', codeql)
        self.assertNotIn('  push:', codeql)
        self.assertNotIn('  schedule:', codeql)
        self.assertNotIn('  workflow_dispatch:', codeql)
        self.assertNotIn('  workflow_call:', codeql)
        self.assertIn('actions/dependency-review-action@v4', validation)
        self.assertIn('name: Dependabot', validation)
        self.assertIn('uses: ./.github/actions/build-validation', validation)
        self.assertIn('uses: ./.github/actions/build-validation', validation)
        self.assertIn('runtime-candidate-', validation)
        self.assertNotIn('  workflow_dispatch:', validation)
        self.assertNotIn('  workflow_call:', validation)
        self.assertIn('make test-e2e', test_action)
        self.assertEqual(validation.count('  auto_merge:\n'), 1)
        self.assertIn('needs: [build_candidate, quality, android, tests, dependency_review]', validation)
        self.assertIn('name: E2E Test Suite', validation)
        self.assertIn('workflow == "CodeQL"', validation)
        self.assertIn('.event == "pull_request"', validation)
        self.assertIn('analyze (', validation)
        self.assertNotIn('workflow == "Dependabot Dependency Review"', validation)
        self.assertIn('length == 2 and all(.[]; .state == "SUCCESS")', validation)
        self.assertIn('gh pr checks "$PR_NUMBER" --repo "$REPOSITORY"', validation)
        self.assertIn('gh pr merge "$PR_NUMBER" --repo "$REPOSITORY"', validation)
        self.assertIn('gh pr merge "$PR_NUMBER" --repo "$REPOSITORY" --merge --auto', validation)
        self.assertIn('on:\n  push:\n    branches: [main]', build)

    def test_pr_candidate_build_is_reusable_and_cannot_publish(self):
        root = pathlib.Path(__file__).resolve().parents[2]
        validation = (root / '.github/workflows/pull_request_validation.yml').read_text()
        build = (root / '.github/workflows/build_publish.yml').read_text()
        self.assertIn('uses: ./.github/actions/build-validation', validation)
        self.assertIn('uses: ./.github/actions/build-validation', validation)
        self.assertIn("if: inputs.operation == 'validate'", build)
        self.assertIn('uses: ./.github/actions/build-validation', build)
        self.assertIn('runtime-candidate-${{ github.run_id }}-${{ github.run_attempt }}', build)
        self.assertIn('docker save alreadyon-ci:candidate', build)
        self.assertIn('workflow_dispatch', build)
        self.assertIn("github.event_name == 'push'", build)

    def test_final_workflow_inventory_and_daily_base_retention(self):
        root = pathlib.Path(__file__).resolve().parents[2]
        workflow_dir = root / '.github/workflows'
        self.assertEqual({path.name for path in workflow_dir.glob('*.yml')}, {
            'android_release.yml', 'build_publish.yml', 'codeql.yml',
            'daily_base_image.yml', 'publish_github_pages.yml',
            'pull_request_validation.yml', 'security_scan.yml', 'weekly_cleanup.yml',
        })
        daily = (workflow_dir / 'daily_base_image.yml').read_text()
        self.assertIn('cron: "43 6 * * *"', daily)
        self.assertIn('clean-base-images.py --apply', daily)
        self.assertIn('refresh: "true"', daily)


class RetentionPolicy(unittest.TestCase):
    """Deletion boundaries are tested without accessing the real package."""

    def setUp(self):
        self.module = load('clean-images')
        self.today = datetime.date(2026, 10, 5)

    def keep(self, tags, open_pr=lambda _: False, active_run=lambda _: False):
        return self.module.retain(tags, self.today, open_pr, active_run)

    def test_active_channels_never_expire(self):
        for channel in ['nightly', 'latest']:
            self.assertIsNotNone(self.keep([channel, '20.01.01', 'sha-' + SHA]))

    def test_date_boundary_and_multiple_dates(self):
        for days, protected in [(89, True), (90, True), (91, False)]:
            day = (self.today - datetime.timedelta(days=days)).isoformat()
            self.assertEqual(self.keep([day]) is not None, protected)
        self.assertIsNotNone(self.keep(['20.01.01', '26.10.05']))

    def test_recent_expired_images_are_reserved_after_age_window(self):
        versions = [
            {'name': f'digest-{index}',
             'created_at': (datetime.datetime.combine(
                 self.today - datetime.timedelta(days=days), datetime.time(),
                 datetime.timezone.utc).isoformat()),
             'metadata': {'container': {'tags': []}}}
            for index, days in enumerate([91, 92, 93, 94, 95, 96])
        ]
        recent = self.module.recent_expired_versions(versions, self.today)
        self.assertEqual(recent, {f'digest-{index}' for index in range(5)})
        self.assertIsNotNone(self.module.retain(['25.07.01'], self.today,
                            lambda _: False, lambda _: False,
                            recent_expired=True))

    def test_release_retention_keeps_channels_recent_and_five_expired(self):
        releases = load('clean-releases')

        def entry(release_id, day, channel, *, draft=False, tag=None):
            date = self.today - datetime.timedelta(days=day)
            suffix = f'{channel}1' if channel in {'alpha', 'beta', 'rc'} else channel
            return {
                'id': release_id,
                'tag_name': tag or f'{date:%y.%m.%d}-{suffix}',
                'draft': draft,
                'prerelease': channel != 'stable',
                'published_at': datetime.datetime.combine(
                    date, datetime.time(12), datetime.timezone.utc).isoformat(),
            }

        items = [entry(1, 200, 'nightly'), entry(2, 220, 'stable'),
                 entry(3, 10, 'alpha')]
        items.extend(entry(index + 4, 91 + index, 'rc') for index in range(6))
        items.extend(entry(index + 30, 300 + index, 'nightly') for index in range(6))
        items.extend(entry(index + 40, 320 + index, 'stable') for index in range(6))
        items.extend([entry(20, 300, 'rc', draft=True),
                      entry(21, 400, 'rc', tag='unrecognized')])
        plan = releases.retention_plan(items, self.today)
        self.assertIn('latest nightly', plan[1])
        self.assertIn('latest stable', plan[2])
        self.assertIn('within retention window', plan[3])
        self.assertTrue(all(plan[index + 4] is not None for index in range(5)))
        self.assertIsNone(plan[9])
        self.assertTrue(all(plan[index + 30] is not None for index in range(5)))
        self.assertIsNone(plan[35])
        self.assertTrue(all(plan[index + 40] is not None for index in range(5)))
        self.assertIsNone(plan[45])
        self.assertIn('draft', plan[20])
        self.assertIn('unrecognized', plan[21])
        source_tags = [
            (self.today - datetime.timedelta(days=days)).strftime('%y.%m.%d')
            for days in [20, 91, 92, 93, 94, 95, 96]
        ]
        source_plan = releases.source_tag_plan(source_tags, self.today)
        self.assertIn('within retention window', source_plan[source_tags[0]])
        self.assertTrue(all(source_plan[tag] is not None for tag in source_tags[1:6]))
        self.assertIsNone(source_plan[source_tags[6]])

    def test_release_cleanup_dry_run_never_deletes(self):
        path = str(pathlib.Path(__file__).with_name('clean-releases.py'))
        entries = [
            {'id': index, 'tag_name': f'25.01.{index:02d}-rc1', 'draft': False,
             'prerelease': True, 'published_at': f'2025-01-{index:02d}T12:00:00Z'}
            for index in range(1, 7)
        ]
        def gh_api(command, **_):
            return json.dumps([] if 'matching-refs/tags/' in command[-1] else [entries])

        with patch.dict(os.environ, GITHUB_REPOSITORY='owner/repo'), \
            patch('subprocess.check_output', side_effect=gh_api), \
                patch('subprocess.run') as delete, \
                patch.object(sys, 'argv', [path]), redirect_stdout(io.StringIO()):
            runpy.run_path(path, run_name='__main__')
        delete.assert_not_called()

    def test_release_cleanup_apply_deletes_only_expired_release_and_tag(self):
        path = str(pathlib.Path(__file__).with_name('clean-releases.py'))
        entries = [
            {'id': 1, 'tag_name': '24.01.01-nightly', 'draft': False,
             'prerelease': True, 'published_at': '2024-01-01T12:00:00Z'},
            {'id': 2, 'tag_name': '24.01.02-stable', 'draft': False,
             'prerelease': False, 'published_at': '2024-01-02T12:00:00Z'},
        ]
        entries.extend({
            'id': index + 3, 'tag_name': f'25.01.{index:02d}-rc1', 'draft': False,
            'prerelease': True, 'published_at': f'2025-01-{index:02d}T12:00:00Z',
        } for index in range(1, 7))
        ref = subprocess.CompletedProcess([], 0, '{}', '')
        def gh_api(command, **_):
            return json.dumps([] if 'matching-refs/tags/' in command[-1] else [entries])

        with patch.dict(os.environ, GITHUB_REPOSITORY='owner/repo'), \
            patch('subprocess.check_output', side_effect=gh_api), \
                patch('subprocess.run', side_effect=[ref, None, None]) as delete, \
                patch.object(sys, 'argv', [path, '--apply']), redirect_stdout(io.StringIO()):
            runpy.run_path(path, run_name='__main__')
        self.assertIn('25.01.01-rc1', delete.call_args_list[1].args[0][-1])
        self.assertIn('/git/refs/tags/25.01.01-rc1', delete.call_args_list[1].args[0][-1])
        self.assertIn('/releases/4', delete.call_args_list[2].args[0][-1])

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
        for utc, day in [('2026-01-02T04:59:59+00:00', '26.01.01'),
                         ('2026-01-02T05:00:00+00:00', '26.01.02'),
                         ('2026-07-02T03:59:59+00:00', '26.07.01'),
                         ('2026-07-02T04:00:00+00:00', '26.07.02'),
                         ('2026-03-08T07:00:00+00:00', '26.03.08'),
                         ('2026-11-01T06:00:00+00:00', '26.11.01')]:
            self.assertTrue(tags('owner/repo', 'main', SHA, datetime.datetime.fromisoformat(utc))[1].endswith(day))


class BaseRetentionPolicy(unittest.TestCase):
    def setUp(self):
        self.module = load('clean-base-images')
        self.cutoff = datetime.datetime(2026, 9, 5, tzinfo=datetime.timezone.utc)

    def test_only_old_recognized_nonchannel_base_images_are_deletable(self):
        def version(tags, created, digest):
            return {'name': digest, 'created_at': created,
                    'metadata': {'container': {'tags': tags}}}

        old = version([], '2026-09-04T00:00:00Z', 'old')
        recent = version([], '2026-09-06T00:00:00Z', 'recent')
        current = version(['base-runtime'], '2026-08-01T00:00:00Z', 'current')
        unknown = version(['manual-tag'], '2026-08-01T00:00:00Z', 'unknown')
        protected = {'current'}
        self.assertTrue(self.module.deletable(old, self.cutoff, protected))
        self.assertFalse(self.module.deletable(recent, self.cutoff, protected))
        self.assertFalse(self.module.deletable(current, self.cutoff, protected))
        self.assertFalse(self.module.deletable(unknown, self.cutoff, protected))


    def test_main_date_alias_uses_current_or_explicit_release_date(self):
        instant = datetime.datetime(2026, 10, 11, 12, tzinfo=datetime.timezone.utc)
        result = tags('owner/repo', 'main', SHA, instant, release_date='26.10.10')
        self.assertEqual(result[1], 'ghcr.io/owner/repo:26.10.10')
        with self.assertRaisesRegex(ValueError, 'valid YY.MM.DD'):
            tags('owner/repo', 'main', SHA, instant, release_date='26.02.30')

    def test_daily_tags_are_shared_and_replaceable(self):
        instant = datetime.datetime(2026, 10, 1, 12, tzinfo=datetime.timezone.utc)
        self.assertEqual(tags('o/r', 'main', SHA, instant),
                         tags('o/r', 'main', SHA, instant.replace(hour=13)))
        self.assertEqual(tags('o/r', 'main', SHA, instant, publication='latest'), ['ghcr.io/o/r:latest'])

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
                        IMAGE_TAGS='ghcr.io/owner/repo:nightly ghcr.io/owner/repo:26.10.05'), \
                patch.object(image_registry, 'record') as record, \
                patch('subprocess.check_output', return_value='Digest: ' + digest), \
                patch('subprocess.run') as docker, \
                patch.object(sys, 'argv', [path, '--local']):
            runpy.run_path(path, run_name='__main__')
            self.assertEqual([call.args[0][1] for call in docker.call_args_list],
                             ['tag', 'push', 'tag', 'push'])
            record.assert_called_once_with('owner/repo', SHA, digest)

    def test_weekly_cleanup_and_manual_nightly_publication(self):
        root = pathlib.Path(__file__).resolve().parents[2]
        cleanup = (root / '.github/workflows/weekly_cleanup.yml').read_text()
        self.assertNotIn('pull_request_target:', cleanup)
        self.assertNotIn('workflow_run:', cleanup)
        self.assertIn('cron: "43 7 * * 1"', cleanup)
        build = (root / '.github/workflows/build_publish.yml').read_text()
        self.assertIn('workflow_dispatch:', build)
        self.assertNotIn('  schedule:', build)
        self.assertIn('workflow_call:', build)
        self.assertIn('dev|main', build)


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
