"""Exercise real Git promotion graphs and the issue-branch workflow helper."""
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


def load(name, directory=None):
    """Import CLI modules without executing their entry points."""
    path = (directory or Path(__file__).parent) / (name + '.py')
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


policy = load('branch-policy')
work = load('work', Path(__file__).parents[1])
promote = load('promote', Path(__file__).parents[1])


class PromotionGraphs(unittest.TestCase):
    """Real histories catch incorrect assumptions about merge commit ancestry."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.cwd = os.getcwd()
        os.chdir(self.tmp.name)
        self.addCleanup(lambda: os.chdir(self.cwd))
        self.addCleanup(self.tmp.cleanup)
        self.git('init', '-q')
        self.git('config', 'user.name', 'Policy Test')
        self.git('config', 'user.email', 'policy@example.invalid')
        self.commit('base')
        self.git('branch', 'test')
        self.git('checkout', '-qb', 'dev')

    def git(self, *args):
        return subprocess.check_output(['git', *args], text=True, stderr=subprocess.DEVNULL).strip()

    def commit(self, content, name='source.txt'):
        Path(name).write_text(content)
        self.git('add', '.')
        self.git('commit', '-qm', content)
        return self.git('rev-parse', 'HEAD')

    def test_selected_older_dev_snapshot_and_second_promotion(self):
        selected = self.commit('first')
        self.commit('second')
        self.git('checkout', '-q', 'test')
        self.git('merge', '--no-ff', '-qm', 'promote first', selected)
        policy.validate_promotion('test', selected, 'dev', 'HEAD')
        # Test's merge commit is not a dev ancestor; a later exact-tree merge is valid.
        self.git('merge', '--no-ff', '-qm', 'promote second', 'dev')
        policy.validate_promotion('test', self.git('rev-parse', 'dev'), 'dev', 'HEAD')

    def test_unrelated_commit_rejected(self):
        selected = self.commit('dev work')
        self.git('checkout', '-q', 'test')
        other = self.commit('unrelated work')
        with self.assertRaisesRegex(ValueError, 'source branch'):
            policy.validate_promotion('test', other, selected, other)

    def test_modified_merge_tree_rejected(self):
        selected = self.commit('selected')
        self.commit('extra merge change')
        with self.assertRaisesRegex(ValueError, 'changes the selected'):
            policy.validate_promotion('test', selected, 'dev', 'HEAD')

    def test_main_requires_current_test_head(self):
        selected = self.commit('old test')
        newer = self.commit('new test')
        with self.assertRaisesRegex(ValueError, 'current test head'):
            policy.validate_promotion('main', selected, newer, selected)
        policy.validate_promotion('main', newer, newer, newer)

    def test_exact_head_baseline_sync(self):
        selected = self.commit('baseline')
        self.assertEqual(policy.promotion_source(selected, selected), selected)
        policy.validate_promotion('main', selected, selected, selected)

    def test_fast_forward_promotion_only_accepts_tested_ancestry(self):
        older = self.commit('old')
        newer = self.commit('new')
        promote.validate_fast_forward(older, newer, newer, 'test')
        with self.assertRaisesRegex(ValueError, 'not in current'):
            promote.validate_fast_forward(older, newer, older, 'main')

    def test_direct_promotion_merge_preserves_source_tree_and_build_branch(self):
        self.git('checkout', '-q', 'test')
        self.git('commit', '--allow-empty', '-qm', 'test branch advanced')
        target = self.git('rev-parse', 'HEAD')
        self.git('checkout', '-q', 'dev')
        selected = self.commit('release change', 'release.txt')
        result = promote.promotion_commit('test', target, selected, 'dev')
        self.assertEqual(self.git('branch', '--show-current'), 'dev')
        self.assertEqual(self.git('rev-parse', f'{result}^{{tree}}'),
                         self.git('rev-parse', f'{selected}^{{tree}}'))
        parents = self.git('rev-list', '--parents', '-n', '1', result).split()[1:]
        self.assertEqual(parents, [target, selected])

    def test_conflicting_direct_promotion_aborts_and_restores_build_branch(self):
        self.git('checkout', '-q', 'test')
        target = self.commit('test edit')
        self.git('checkout', '-q', 'dev')
        selected = self.commit('dev edit')
        with self.assertRaises(subprocess.CalledProcessError):
            promote.promotion_commit('test', target, selected, 'dev')
        self.assertEqual(self.git('branch', '--show-current'), 'dev')
        self.assertEqual(self.git('status', '--porcelain'), '')

    def test_arbitrary_nonmerge_push_is_rejected(self):
        selected = self.commit('earlier snapshot')
        upstream = self.commit('current source')
        with self.assertRaisesRegex(ValueError, 'merge commit or the exact upstream head'):
            policy.promotion_source(selected, upstream)

    def test_dev_pr_must_contain_current_dev(self):
        base = self.git('rev-parse', 'dev')
        self.git('checkout', '-qb', 'feature/x')
        head = self.commit('feature work', 'feature.txt')
        policy.validate_up_to_date(head, base)
        self.git('checkout', '-q', 'dev')
        newer_dev = self.commit('dev advanced', 'dev.txt')
        with self.assertRaisesRegex(ValueError, 'current dev head'):
            policy.validate_up_to_date(head, newer_dev)
        self.git('checkout', '-q', 'feature/x')
        self.git('merge', '--no-ff', '-qm', 'update from dev', newer_dev)
        policy.validate_up_to_date(self.git('rev-parse', 'HEAD'), newer_dev)

    def test_issue_branch_conventions(self):
        for name in ['feature/enable_tls_support', 'bugfix/h.265_encode_failure']:
            policy.validate_work_branch(name, ['server/main.go', 'README.md'])
        policy.validate_work_branch('26-10-10', ['server/main.go', 'README.md'])
        policy.validate_work_branch('26-10-10', ['docs/RELEASE_NOTES.md'])
        with self.assertRaisesRegex(ValueError, 'valid YY-MM-DD'):
            policy.validate_work_branch('26-02-30', ['server/main.go'])
        policy.validate_work_branch('docs/developer_howto', ['docs/DEVELOPMENT.md'])
        for name, files in [('fix/a', ['server/main.go']), ('docs/a', ['scripts/run.sh']),
                            ('feature/a', ['README.md'])]:
            with self.assertRaises(ValueError):
                policy.validate_work_branch(name, files)


class SharedWorkflow(unittest.TestCase):
    """The shared checkout helper must preserve existing work."""
    def test_dirty_tree_refuses_switch(self):
        from unittest.mock import patch
        with patch.object(work.subprocess, 'check_output', return_value=' M README.md'), patch('sys.argv', ['work.py', 'start']):
            with self.assertRaises(SystemExit):
                work.main()

if __name__ == '__main__':
    unittest.main()
