"""Exercise isolated issue-branch creation against local Git repositories."""
import pathlib
import importlib.util
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = pathlib.Path(__file__).resolve().parents[1] / 'work.py'


class WorkBranchTest(unittest.TestCase):
    def test_publish_creates_date_branch_tag_and_dev_pr(self):
        spec = importlib.util.spec_from_file_location('work_helper', SCRIPT)
        work_helper = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(work_helper)
        with patch.object(work_helper, 'git', side_effect=['', 'build', 'a' * 40, 'a' * 40]), \
                patch.object(work_helper, 'ancestor', return_value=True), \
                patch.object(work_helper.Path, 'read_text', return_value='2026-10-11\n'), \
                patch.object(work_helper, 'release_tags', return_value=[]), \
                patch.object(work_helper, 'remote_ref_sha', return_value=None), \
                patch.object(work_helper.subprocess, 'run') as commands, \
                patch.object(work_helper.subprocess, 'check_output', return_value='[]'):
            work_helper.publish_release_branch('2026-10-11')
        invocations = [call.args[0] for call in commands.call_args_list]
        self.assertIn(['git', 'push', 'origin', 'HEAD:refs/heads/2026-10-11'], invocations)
        self.assertIn(['git', 'tag', '-f', '2026-10-11', 'a' * 40], invocations)
        self.assertTrue(any(call[:4] == ['gh', 'pr', 'create', '--base'] for call in invocations))

    def test_publish_blocks_new_date_while_release_active(self):
        spec = importlib.util.spec_from_file_location('work_helper', SCRIPT)
        work_helper = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(work_helper)
        with patch.object(work_helper, 'git', side_effect=['', 'build', 'a' * 40, 'a' * 40]), \
                patch.object(work_helper, 'ancestor', return_value=True), \
                patch.object(work_helper.Path, 'read_text', return_value='2026-10-11\n'), \
                patch.object(work_helper, 'release_tags', return_value=['2026-10-11']), \
                patch.object(work_helper.subprocess, 'run') as commands:
            with self.assertRaisesRegex(SystemExit, 'must reach Stable'):
                work_helper.publish_release_branch('2026-10-12')
        self.assertFalse(any(call.args[0][1] == 'push' for call in commands.call_args_list))

    def test_active_release_date_blocks_a_new_release_date(self):
        from importlib.util import module_from_spec, spec_from_file_location
        spec = spec_from_file_location('work_helper', SCRIPT)
        work_helper = module_from_spec(spec)
        spec.loader.exec_module(work_helper)
        self.assertEqual(work_helper.active_release_day(['2026-10-10', '2026-10-10-alpha1']),
                         '2026-10-10')
        self.assertIsNone(work_helper.active_release_day(['2026-10-10', '2026-10-10-Stable']))
        self.assertIsNone(work_helper.active_release_day(['2026-10-10', '2026-10-10-stable']))
        with self.assertRaisesRegex(ValueError, 'Multiple date releases'):
            work_helper.active_release_day(['2026-10-10', '2026-10-11'])
        self.assertEqual(work_helper.active_release_day(['2026-10-10'], '2026-10-11'), '2026-10-11')
        self.assertIsNone(work_helper.active_release_day(['2026-10-10', '2026-10-11', '2026-10-11-Stable'], '2026-10-11'))

    def test_stale_build_fast_forwards_to_latest_dev(self):
        with tempfile.TemporaryDirectory() as root:
            root = pathlib.Path(root)
            remote, repo = root / 'remote.git', root / 'work'
            subprocess.run(['git', 'init', '--bare', str(remote)], check=True, capture_output=True)
            repo.mkdir()

            def git(*args):
                return subprocess.check_output(['git', *args], cwd=repo, stderr=subprocess.DEVNULL, text=True).strip()

            git('init', '-b', 'dev')
            git('config', 'user.name', 'Fixture')
            git('config', 'user.email', 'fixture@example.invalid')
            git('commit', '--allow-empty', '-m', 'baseline')
            git('remote', 'add', 'origin', str(remote))
            git('push', '-u', 'origin', 'dev')
            subprocess.run(['python3', str(SCRIPT), 'start', 'build'], cwd=repo, check=True,
                           text=True, capture_output=True)
            git('switch', 'dev')
            git('commit', '--allow-empty', '-m', 'dev advanced')
            git('push', 'origin', 'dev')
            subprocess.run(['python3', str(SCRIPT), 'start', 'build'], cwd=repo, check=True,
                           text=True, capture_output=True)
            self.assertEqual(git('branch', '--show-current'), 'build')
            self.assertEqual(git('rev-parse', 'HEAD'), git('rev-parse', 'origin/dev'))

    def test_unique_build_merges_new_dev_commits(self):
        with tempfile.TemporaryDirectory() as root:
            root = pathlib.Path(root)
            remote, repo = root / 'remote.git', root / 'work'
            subprocess.run(['git', 'init', '--bare', str(remote)], check=True, capture_output=True)
            repo.mkdir()

            def git(*args):
                return subprocess.check_output(['git', *args], cwd=repo, stderr=subprocess.DEVNULL, text=True).strip()

            git('init', '-b', 'dev')
            git('config', 'user.name', 'Fixture')
            git('config', 'user.email', 'fixture@example.invalid')
            pathlib.Path(repo / 'base.txt').write_text('base')
            git('add', '.')
            git('commit', '-qm', 'baseline')
            git('remote', 'add', 'origin', str(remote))
            git('push', '-u', 'origin', 'dev')
            subprocess.run(['python3', str(SCRIPT), 'start', 'build'], cwd=repo, check=True,
                           text=True, capture_output=True)
            pathlib.Path(repo / 'build-work.txt').write_text('build')
            git('add', '.')
            git('commit', '-qm', 'local work')
            git('switch', 'dev')
            pathlib.Path(repo / 'dev-work.txt').write_text('dev')
            git('add', '.')
            git('commit', '-qm', 'dev update')
            git('push', 'origin', 'dev')
            subprocess.run(['python3', str(SCRIPT), 'start', 'build'], cwd=repo, check=True,
                           text=True, capture_output=True)
            self.assertEqual(git('branch', '--show-current'), 'build')
            self.assertTrue((repo / 'build-work.txt').exists())
            self.assertTrue((repo / 'dev-work.txt').exists())

    def test_start_assistant_branches_from_current_dev(self):
        with tempfile.TemporaryDirectory() as root:
            root = pathlib.Path(root)
            remote, repo = root / 'remote.git', root / 'work'
            subprocess.run(['git', 'init', '--bare', str(remote)], check=True, capture_output=True)
            repo.mkdir()

            def git(*args):
                return subprocess.check_output(['git', *args], cwd=repo, stderr=subprocess.DEVNULL, text=True).strip()

            git('init', '-b', 'dev')
            git('config', 'user.name', 'Fixture')
            git('config', 'user.email', 'fixture@example.invalid')
            git('commit', '--allow-empty', '-m', 'baseline')
            git('remote', 'add', 'origin', str(remote))
            git('push', '-u', 'origin', 'dev')

            def start(name):
                return subprocess.check_output(['python3', str(SCRIPT), 'start', name], cwd=repo, text=True)

            start('build')
            self.assertEqual(git('branch', '--show-current'), 'build')
            git('commit', '--allow-empty', '-m', 'pending fix')
            head = git('rev-parse', 'HEAD')
            git('switch', 'dev')
            start('build')
            self.assertEqual(git('rev-parse', 'HEAD'), head)

    def test_assistant_description_must_be_two_or_three_words(self):
        with tempfile.TemporaryDirectory() as root:
            root = pathlib.Path(root)
            remote, repo = root / 'remote.git', root / 'work'
            subprocess.run(['git', 'init', '--bare', str(remote)], check=True, capture_output=True)
            repo.mkdir()
            subprocess.run(['git', 'init', '-b', 'dev'], cwd=repo, check=True, capture_output=True)
            subprocess.run(['git', 'remote', 'add', 'origin', str(remote)], cwd=repo, check=True)
            result = subprocess.run(
                ['python3', str(SCRIPT), 'start', 'codex/too-many-description-words'],
                cwd=repo, text=True, capture_output=True,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('start only accepts the local build branch', result.stderr)


if __name__ == '__main__':
    unittest.main()
