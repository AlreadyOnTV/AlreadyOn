"""Release context tests exercise date resolution and guarded tag writes."""
from importlib.util import module_from_spec, spec_from_file_location
import json
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

SPEC = spec_from_file_location("release_context", Path(__file__).with_name("release-context.py"))
release_context = module_from_spec(SPEC)
SPEC.loader.exec_module(release_context)


class ReleaseContextTests(unittest.TestCase):
    def test_active_branch_build_resolves_without_a_merged_pr(self):
        with patch.object(release_context, 'remote_ref', return_value=None):
            self.assertEqual(release_context.build_date('owner/repo', 'a' * 40,
                '2026-10-18', '2026-10-18\n'), '2026-10-18')

    def test_unrelated_branch_and_conflicting_release_date_are_rejected(self):
        for branch, requested in [('main', None), ('2026-10-17', None),
                                  ('2026-10-18', '2026-10-19')]:
            with self.subTest(branch=branch, requested=requested), self.assertRaises(ValueError):
                release_context.build_date('owner/repo', 'a' * 40, branch,
                                           '2026-10-18', requested)

    def test_date_pr_is_resolved_from_merged_pr_head(self):
        sha = "a" * 40
        pulls = [
            {"base": {"ref": "dev"}, "merged_at": "2026-10-10T12:00:00Z",
             "merge_commit_sha": sha, "head": {"ref": "2026-10-10"}},
            {"base": {"ref": "test"}, "merged_at": "2026-10-10T12:00:00Z",
             "merge_commit_sha": sha, "head": {"ref": "promotion/test-123"}},
        ]
        with patch.object(release_context, "run", return_value=json.dumps(pulls)), \
            patch.object(release_context, "remote_ref", return_value=None):
            self.assertEqual(release_context.resolve_date("owner/repo", sha), "2026-10-10")

    def test_date_resolution_requires_one_merged_date_pr(self):
        with patch.object(release_context, "run", return_value="[]"):
            with self.assertRaisesRegex(ValueError, "exactly one merged"):
                release_context.resolve_date("owner/repo", "a" * 40)

    def test_explicit_date_is_validated(self):
        with patch.object(release_context, "remote_ref", return_value=None):
            self.assertEqual(release_context.resolve_date("owner/repo", "a" * 40, "2026-10-10"),
                             "2026-10-10")
        with self.assertRaisesRegex(ValueError, "valid YYYY-MM-DD"):
            release_context.resolve_date("owner/repo", "a" * 40, "2026-02-30")

    def test_stable_date_cannot_be_resolved_for_new_artifacts(self):
        with patch.object(release_context, "remote_ref", side_effect=["stable-sha", None]):
            with self.assertRaisesRegex(ValueError, "is stable"):
                release_context.resolve_date("owner/repo", "a" * 40, "2026-10-10")

    def test_promoted_source_uses_newest_reachable_date_tag(self):
        class GitRepo:
            def __enter__(self):
                self.temp = __import__('tempfile').TemporaryDirectory()
                self.cwd = __import__('os').getcwd()
                __import__('os').chdir(self.temp.name)
                subprocess.run(['git', 'init', '-q'], check=True)
                subprocess.run(['git', 'config', 'user.name', 'Fixture'], check=True)
                subprocess.run(['git', 'config', 'user.email', 'fixture@example.invalid'], check=True)
                subprocess.run(['git', 'commit', '--allow-empty', '-qm', 'release'], check=True)
                subprocess.run(['git', 'tag', '2026-10-09'], check=True)
                subprocess.run(['git', 'commit', '--allow-empty', '-qm', 'hotfix'], check=True)
                subprocess.run(['git', 'tag', '2026-10-10'], check=True)
                self.head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
                return self

            def __exit__(self, *_):
                __import__('os').chdir(self.cwd)
                self.temp.cleanup()

        with GitRepo() as repo:
            self.assertEqual(release_context.date_for_source(repo.head), '2026-10-10')

    def test_missing_ref_detects_github_404(self):
        result = subprocess.CompletedProcess(
            ["gh", "api"], 1, '{"message":"Not Found","status":"404"}', "gh: Not Found")
        with patch.object(release_context.subprocess, "run", return_value=result):
            self.assertIsNone(release_context.remote_ref("owner/repo", "tags/2026-10-10"))

    def test_source_tag_is_created_when_missing(self):
        sha = "a" * 40
        with patch.object(release_context, "remote_ref", return_value=None), \
                patch.object(release_context.subprocess, "run") as command:
            release_context.update_source_tag("owner/repo", "2026-10-10", sha)
        self.assertEqual(command.call_args.args[0][4], "repos/owner/repo/git/refs")
        payload = json.loads(command.call_args.kwargs["input"])
        self.assertEqual(payload, {"ref": "refs/tags/2026-10-10", "sha": sha})

    def test_source_tag_advances_only_without_force(self):
        old, new = "a" * 40, "b" * 40
        responses = [None, None, old]
        results = [subprocess.CompletedProcess(["git"], 0), subprocess.CompletedProcess(["gh"], 0)]
        with patch.object(release_context, "remote_ref", side_effect=responses), \
                patch.object(release_context.subprocess, "run", side_effect=results) as command:
            release_context.update_source_tag("owner/repo", "2026-10-10", new)
        payload = json.loads(command.call_args.kwargs["input"])
        self.assertEqual(payload, {"sha": new, "force": False})

    def test_source_tag_update_requires_ancestor_and_no_stable(self):
        with patch.object(release_context, "remote_ref", side_effect=[None, None, "b" * 40]), \
                patch.object(release_context.subprocess, "run",
                             return_value=subprocess.CompletedProcess(["git"], 1)):
            with self.assertRaisesRegex(ValueError, "descend"):
                release_context.update_source_tag("owner/repo", "2026-10-10", "a" * 40)
        with patch.object(release_context, "remote_ref", return_value="c" * 40):
            with self.assertRaisesRegex(ValueError, "is stable"):
                release_context.update_source_tag("owner/repo", "2026-10-10", "a" * 40)

    def test_cleanup_branch_deletes_only_merged_closed_date_branch(self):
        calls = [subprocess.CompletedProcess(["git"], 0), subprocess.CompletedProcess(["gh"], 0)]
        with patch.object(release_context, "remote_ref", return_value="b" * 40), \
                patch.object(release_context.subprocess, "run", side_effect=calls) as run, \
                patch.object(release_context.subprocess, "check_output", return_value="[]"):
            self.assertTrue(release_context.cleanup_release_branch("owner/repo", "2026-10-10", "a" * 40))
        self.assertIn("DELETE", run.call_args.args[0])

    def test_cleanup_branch_keeps_open_or_unmerged_branch(self):
        with patch.object(release_context, "remote_ref", return_value="b" * 40), \
                patch.object(release_context.subprocess, "run",
                             return_value=subprocess.CompletedProcess(["git"], 1)), \
                patch.object(release_context.subprocess, "check_output") as command:
            self.assertFalse(release_context.cleanup_release_branch("owner/repo", "2026-10-10", "a" * 40))
        command.assert_not_called()

    def test_release_branch_cleanup_requires_merged_tip_and_no_open_pr(self):
        with patch.object(release_context, "remote_ref", return_value="b" * 40), \
                patch.object(release_context.subprocess, "run", side_effect=[
                    subprocess.CompletedProcess(["git"], 0), subprocess.CompletedProcess(["gh"], 0)]
                ) as command, \
                patch.object(release_context.subprocess, "check_output", return_value="[]"):
            self.assertTrue(release_context.cleanup_release_branch("owner/repo", "2026-10-10", "a" * 40))
        self.assertIn("DELETE", command.call_args.args[0])

    def test_release_branch_cleanup_keeps_unmerged_branch(self):
        with patch.object(release_context, "remote_ref", return_value="b" * 40), \
                patch.object(release_context.subprocess, "run",
                             return_value=subprocess.CompletedProcess(["git"], 1)), \
                patch.object(release_context.subprocess, "check_output") as command:
            self.assertFalse(release_context.cleanup_release_branch("owner/repo", "2026-10-10", "a" * 40))
        command.assert_not_called()


if __name__ == "__main__":
    unittest.main()
