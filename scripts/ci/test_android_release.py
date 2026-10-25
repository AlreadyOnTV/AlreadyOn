"""Release policy tests use synthetic APK bytes and mocked GitHub operations."""
import importlib.util
import json
from datetime import datetime
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("android_release", Path(__file__).with_name("android-release.py"))
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)


class AndroidReleaseTests(unittest.TestCase):
    def fixture(self, directory, tag="26.10.09-alpha1"):
        (directory / release.ASSETS[0]).write_bytes(b"synthetic signed APK")
        (directory / release.ASSETS[3]).write_bytes(b"synthetic signed TV APK")
        with patch.object(release, "publish"), patch.object(release, "releases", return_value=[]), \
                patch.object(release, "candidate_changes", return_value="## Features\n\n- Synthetic feature."):
            release.candidate("owner/repo", tag, "a" * 40, 200000000, directory)

    def test_short_year_20_is_not_confused_with_a_century_prefix(self):
        with tempfile.TemporaryDirectory() as temp:
            self.fixture(Path(temp), "20.10.18-alpha1")
            self.assertEqual(release.verify_assets(Path(temp))["version_name"], "20.10.18-alpha1")

    def test_new_candidates_reject_historical_public_labels(self):
        with tempfile.TemporaryDirectory() as temp:
            with self.assertRaisesRegex(ValueError, "YY.MM.DD"):
                self.fixture(Path(temp), "2026-10-18-alpha3")

    def test_candidate_identifies_phone_and_tv_update_assets(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            self.fixture(directory)
            manifest = release.verify_assets(directory)
            self.assertEqual(manifest["artifacts"]["phone"], {"file": release.ASSETS[0], "sha256": release.digest(directory / release.ASSETS[0])})
            self.assertEqual(manifest["artifacts"]["tv"], {"file": release.ASSETS[3], "sha256": release.digest(directory / release.ASSETS[3])})

    def test_cross_target_metadata_cannot_be_promoted(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            self.fixture(directory)
            manifest = json.loads((directory / "android-release.json").read_text())
            manifest["artifacts"]["tv"] = manifest["artifacts"]["phone"]
            (directory / "android-release.json").write_text(json.dumps(manifest))
            with self.assertRaisesRegex(ValueError, "target metadata"):
                release.verify_assets(directory)

    def test_download_includes_both_apks_and_provenance(self):
        with patch.object(release, "run") as command, \
                patch.object(release, "releases", return_value=[{"tag_name": "26.10.09-alpha1", "assets": []}]):
            release.download("owner/repo", "26.10.09-alpha1", Path("assets"))
            arguments = command.call_args.args
            for asset in release.ASSETS:
                self.assertIn(asset, arguments)

    def test_tv_checksum_detects_changed_apk(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            self.fixture(directory)
            (directory / release.ASSETS[3]).write_bytes(b"different TV APK")
            with self.assertRaisesRegex(ValueError, "TV APK checksum"):
                release.verify_assets(directory)

    def test_dev_stage_allocates_alpha_then_rc_and_stable_closes_date(self):
        self.assertEqual(release.next_dev_tag([], "26.10.10"), "26.10.10-alpha1")
        entries = [
            {"tag_name": "26.10.10-beta", "draft": False},
            {"tag_name": "26.10.10-beta1", "draft": False},
            {"tag_name": "26.10.10-alpha1", "draft": False},
        ]
        self.assertEqual(release.next_dev_tag(entries, "26.10.10"), "26.10.10-alpha2")
        entries.append({"tag_name": "26.10.10-RC1", "draft": False})
        self.assertEqual(release.next_dev_tag(entries, "26.10.10"), "26.10.10-RC2")
        entries.append({"tag_name": "26.10.10-RC2", "draft": True})
        self.assertEqual(release.next_dev_tag(entries, "26.10.10"), "26.10.10-RC2")
        entries.append({"tag_name": "26.10.10-Stable", "draft": False})
        with self.assertRaisesRegex(ValueError, "is stable"):
            release.next_dev_tag(entries, "26.10.10")

    def test_reserved_rc1_draft_skips_alpha_without_replacing_published_rc(self):
        entries = [{"tag_name": "26.10.10-alpha4", "draft": False},
                   {"tag_name": "26.10.10-RC1", "draft": True}]
        self.assertEqual(release.next_dev_tag(entries, "26.10.10"), "26.10.10-RC1")
        self.assertEqual(release.next_dev_tag(entries, "26.10.11"), "26.10.11-alpha1")
        entries[-1]["draft"] = False
        self.assertEqual(release.next_dev_tag(entries, "26.10.10"), "26.10.10-RC2")
        entries.append({"tag_name": "26.10.10-Stable", "draft": False})
        with self.assertRaisesRegex(ValueError, "is stable"):
            release.next_dev_tag(entries, "26.10.10")

    def test_cleanup_removes_only_legacy_betas_for_same_date(self):
        entries = [{"tag_name": "26.10.10-beta", "draft": False},
                   {"tag_name": "26.10.10-beta2", "draft": False},
                   {"tag_name": "26.10.09-rc3", "draft": False},
                   {"tag_name": "26.10.09-beta1", "draft": False}]
        with patch.object(release, "releases", return_value=entries), \
                patch.object(release, "run") as command:
            removed = release.cleanup_legacy_betas("owner/repo", "26.10.10")
        self.assertEqual(removed, ["26.10.10-beta", "26.10.10-beta2"])
        self.assertEqual(command.call_count, 2)

    def test_previous_release_uses_latest_candidate_from_prior_date(self):
        entries = [
            {"tag_name": "26.10.10-beta1", "draft": False},
            {"tag_name": "26.10.10-beta2", "draft": False},
            {"tag_name": "26.10.09-rc1", "draft": False},
            {"tag_name": "26.10.09-rc3", "draft": False},
            {"tag_name": "26.10.09-rc2", "draft": False},
            {"tag_name": "26.10.08-stable", "draft": False},
            {"tag_name": "26.10.09-rc4", "draft": True},
        ]
        self.assertEqual(release.previous_release_tag(entries, "26.10.10"), "26.10.09-rc3")

    def test_candidate_changelog_uses_previous_android_release(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            (directory / release.ASSETS[0]).write_bytes(b"synthetic signed APK")
            (directory / release.ASSETS[3]).write_bytes(b"synthetic signed TV APK")
            prior = {"tag_name": "26.10.09-rc3", "draft": False}
            with patch.object(release, "releases", return_value=[prior]), \
                    patch.object(release, "api", return_value={"sha": "b" * 40}), \
                    patch.object(release, "candidate_changes", return_value="## Features\n\n- Enter a server address immediately.") as notes, \
                    patch.object(release, "publish") as published:
                release.candidate("owner/repo", "26.10.10-alpha1", "a" * 40, 200000000, directory)
                notes.assert_called_once_with("owner/repo", [prior], "26.10.10-alpha1", "a" * 40, "26.10.09-rc3")
                self.assertIn("Changelog baseline: `26.10.09-rc3`", published.call_args.args[4])

    def test_new_plan_selects_alpha1_even_with_legacy_betas(self):
        class FixedDateTime:
            @classmethod
            def now(cls, tz):
                return datetime(2026, 10, 10, tzinfo=tz)
        with patch.object(release, "datetime", FixedDateTime), \
                patch.object(release, "releases", return_value=[
                    {"tag_name": "26.10.10-beta", "draft": False},
                    {"tag_name": "26.10.10-beta1", "draft": False},
                ]), \
                patch.object(release, "run", return_value=""), \
                patch("builtins.print") as output:
            release.plan("owner/repo", "a" * 40)
            output.assert_any_call("tag=26.10.10-alpha1")
            output.assert_any_call("stage=alpha")

    def test_plan_increments_alpha_without_overwriting_published_assets(self):
        class FixedDateTime:
            @classmethod
            def now(cls, tz):
                return datetime(2026, 10, 10, tzinfo=tz)
        entry = {"tag_name": "26.10.10-alpha1", "draft": False,
                 "body": f"Source commit: `{'b' * 40}`"}
        with patch.object(release, "datetime", FixedDateTime), \
                patch.object(release, "releases", return_value=[entry]), \
                patch.object(release, "run", return_value=""), \
                patch("builtins.print") as output:
            release.plan("owner/repo", "a" * 40)
            output.assert_any_call("tag=26.10.10-alpha2")

    def test_legacy_rc_assets_remain_verifiable(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            self.fixture(directory, "26.10.09-rc2")
            self.assertEqual(release.verify_assets(directory)["version_name"], "26.10.09-rc2")

    def test_checksums_detect_changed_apk(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            self.fixture(directory)
            release.verify_assets(directory, "a" * 40)
            (directory / release.ASSETS[0]).write_bytes(b"different APK")
            with self.assertRaisesRegex(ValueError, "checksum"):
                release.verify_assets(directory, "a" * 40)

    def test_checksums_detect_changed_manifest(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            self.fixture(directory)
            path = directory / release.ASSETS[2]
            data = json.loads(path.read_text())
            data["version_code"] += 1
            path.write_text(json.dumps(data))
            with self.assertRaisesRegex(ValueError, "checksum"):
                release.verify_assets(directory, "a" * 40)

    def test_provenance_must_match_tag_commit(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            self.fixture(directory)
            with self.assertRaisesRegex(ValueError, "revision"):
                release.verify_assets(directory, "b" * 40)

    def test_stable_cannot_be_built_as_a_candidate(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            (directory / release.ASSETS[0]).write_bytes(b"synthetic APK")
            (directory / release.ASSETS[3]).write_bytes(b"synthetic TV APK")
            with patch.object(release, "publish") as publish:
                with self.assertRaisesRegex(ValueError, "Invalid Android release"):
                    release.candidate("owner/repo", "26.10.10-Stable", "a" * 40, 1, directory)
                publish.assert_not_called()

    def test_direct_stable_requires_exact_source_image_evidence(self):
        with patch.object(release, "image_source", return_value="ghcr.io/owner/repo:sha-unvalidated"), \
                patch.object(release, "publish") as publish:
            with self.assertRaisesRegex(ValueError, "validated image digest"):
                release.candidate("owner/repo", "26.10.10-Stable", "a" * 40, 1, Path("unused"), direct_stable=True)
            publish.assert_not_called()

    def test_direct_stable_build_publishes_stable_bytes_and_notes(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            (directory / release.ASSETS[0]).write_bytes(b"signed phone")
            (directory / release.ASSETS[3]).write_bytes(b"signed TV")
            with patch.object(release, "image_source", return_value="ghcr.io/owner/repo@sha256:" + "d" * 64), \
                    patch.object(release, "releases", return_value=[{"tag_name": "26.10.10-RC1", "draft": False}]), \
                    patch.object(release, "candidate_changes", return_value="# New Features\n\n- Open guide directly.") as changes, \
                    patch.object(release, "publish") as publish:
                release.candidate("owner/repo", "26.10.10-Stable", "a" * 40, 200000001, directory, direct_stable=True)
                manifest = release.verify_assets(directory, "a" * 40)
                self.assertEqual(manifest["version_name"], "26.10.10-Stable")
                self.assertEqual(manifest["version_code"], 200000001)
                self.assertEqual(changes.call_args.args[-1], "26.10.10-RC1")
                self.assertFalse(publish.call_args.args[-1])
                self.assertTrue(publish.call_args.kwargs["publish_now"])
                self.assertIn("Open guide directly", publish.call_args.args[-2])

    def test_direct_stable_allocation_does_not_allocate_an_rc(self):
        with patch.object(release, "image_source", return_value="ghcr.io/owner/repo@sha256:" + "d" * 64), \
                patch.object(release, "releases", return_value=[{"tag_name": "26.10.10-RC1", "draft": False}]), \
                patch.object(release, "run", return_value="") as run, patch("builtins.print") as output:
            release.plan_stable("owner/repo", "a" * 40, "26.10.10")
            output.assert_any_call("tag=26.10.10-Stable")
            output.assert_any_call("stage=stable")
            output.assert_any_call("existing=false")
            run.assert_called_once_with("git", "tag", "--list", "26.10.10-Stable")

    def test_direct_stable_draft_cannot_replace_another_source(self):
        entry = {"tag_name": "26.10.10-Stable", "draft": True, "body": "Source commit: `" + "b" * 40 + "`"}
        with patch.object(release, "image_source", return_value="ghcr.io/owner/repo@sha256:" + "d" * 64), \
                patch.object(release, "releases", return_value=[entry]), patch.object(release, "run") as run:
            with self.assertRaisesRegex(ValueError, "different source"):
                release.plan_stable("owner/repo", "a" * 40, "26.10.10")
            run.assert_not_called()

    def test_complete_stable_draft_resumes_without_rebuilding(self):
        sha = "a" * 40
        entry = {"tag_name": "26.10.10-Stable", "draft": True, "target_commitish": sha,
                 "body": f"Source commit: `{sha}`\nAndroid versionCode: `200000001`",
                 "assets": [{"name": name} for name in release.ASSETS]}
        with patch.object(release, "image_source", return_value="ghcr.io/owner/repo@sha256:" + "d" * 64), \
                patch.object(release, "releases", return_value=[entry]), \
                patch.object(release, "download"), patch.object(release, "verify_assets", return_value={"inventory": {}, "version_name": "26.10.10-Stable", "version_code": 200000001}) as verify, \
                patch.object(release, "run") as run, patch("builtins.print") as output:
            release.plan_stable("owner/repo", sha, "26.10.10")
            self.assertEqual(verify.call_args.args[1], sha)
            output.assert_any_call("existing=true")
            output.assert_any_call("version_code=200000001")
            run.assert_called_once_with("gh", "release", "edit", "26.10.10-Stable", "--repo", "owner/repo",
                                        "--draft=false", "--prerelease=false", "--latest=true")

    def test_stable_branch_sync_reuses_assets_without_an_rc(self):
        sha = "a" * 40
        entry = {"tag_name": "26.10.09-Stable", "draft": False, "body": f"Source commit: `{sha}`"}
        with patch.object(release, "releases", return_value=[entry]), \
                patch.object(release, "api", return_value={"sha": sha}), \
                patch.object(release, "download", side_effect=lambda repo, tag, directory: self.fixture(directory)), \
                patch.object(release, "publish") as publish:
            self.assertEqual(release.promote_test("owner/repo", "26.10.09", sha), entry["tag_name"])
            self.assertEqual(release.promote_stable("owner/repo", "26.10.09", sha), entry["tag_name"])
            publish.assert_not_called()

    def test_branch_sync_rejects_a_stable_from_another_source(self):
        entry = {"tag_name": "26.10.09-Stable", "draft": False, "body": "Source commit: `" + "b" * 40 + "`"}
        with patch.object(release, "releases", return_value=[entry]), patch.object(release, "publish") as publish:
            for promote in (release.promote_test, release.promote_stable):
                with self.assertRaisesRegex(ValueError, "different source"):
                    promote("owner/repo", "26.10.09", "a" * 40)
            publish.assert_not_called()


    def test_legacy_promotion_cannot_copy_apks(self):
        with patch.object(release, "published_stable", return_value=None), patch.object(release, "publish") as publish:
            for promote in (release.promote_test, release.promote_stable):
                with self.assertRaisesRegex(ValueError, "fresh APKs"):
                    promote("owner/repo", "26.10.09", "a" * 40)
            publish.assert_not_called()

    def promotion_fixture(self, stage="stable", repair=False, existing=False, embedded="26.10.09-RC1"):
        sha = "a" * 40
        entry = {"tag_name": "26.10.09-RC1" if stage == "stable" else "26.10.09-alpha1",
                 "draft": False, "body": f"Source commit: `{sha}`\nAndroid versionCode: `200000000`"}
        entries = [entry]
        if existing:
            entries.append(dict(entry, tag_name="26.10.09-Stable"))
        from contextlib import ExitStack
        with ExitStack() as stack:
            stack.enter_context(patch.object(release, "releases", return_value=entries))
            stack.enter_context(patch.object(release, "published_stable", return_value=None))
            stack.enter_context(patch.object(release, "api", return_value={"sha": sha}))
            stack.enter_context(patch.object(release, "download"))
            stack.enter_context(patch.object(release, "verify_assets", return_value={"version_code": 200000000, "version_name": embedded}))
            stack.enter_context(patch.object(release, "require_tested_image"))
            git = stack.enter_context(patch.object(release.subprocess, "run"))
            output = stack.enter_context(patch("builtins.print"))
            release.promotion_plan("owner/repo", "26.10.09", "b" * 40, stage, repair=repair)
            return output, git

    def test_stable_build_defaults_to_rc_commit_not_main_head(self):
        output, git = self.promotion_fixture()
        output.assert_any_call("source_sha=" + "a" * 40)
        output.assert_any_call("tag=26.10.09-Stable")
        git.assert_called_once_with(["git", "merge-base", "--is-ancestor", "a" * 40, "b" * 40], check=True)
        code = int(next(call.args[0].split("=")[1] for call in output.call_args_list if call.args[0].startswith("version_code=")))
        self.assertGreater(code, 200000000)

    def test_repair_rejects_stable_that_already_has_stable_apks(self):
        with self.assertRaisesRegex(ValueError, "own APK build"):
            self.promotion_fixture(repair=True, existing=True, embedded="26.10.09-Stable")

    def test_repair_accepts_only_copied_candidate_release(self):
        output, _ = self.promotion_fixture(repair=True, existing=True)
        output.assert_any_call("existing=false")

    def test_promoted_apks_get_target_name_code_source_and_notes(self):
        sha = "a" * 40
        prior = {"tag_name": "26.10.09-RC1", "draft": False, "body": release.release_body("owner/repo", "26.10.09-RC1", sha, 200000000, "# New Features\n\nNo new features in this release.")}
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            self.fixture(directory)
            with patch.object(release, "api", return_value=prior), \
                    patch.object(release, "download"), \
                    patch.object(release, "verify_assets", return_value={"version_code": 200000000}), \
                    patch.object(release, "require_tested_image"), \
                    patch.object(release, "releases", return_value=[prior]), \
                    patch.object(release, "publish") as published:
                release.publish_promotion("owner/repo", "26.10.09-Stable", sha, 200000001, directory, prior["tag_name"])
            manifest = release.verify_assets(directory, sha)
            self.assertEqual(manifest["version_name"], "26.10.09-Stable")
            self.assertEqual(manifest["version_code"], 200000001)
            self.assertIn("Promoted from: `26.10.09-RC1`", published.call_args.args[4])

    def test_version_allocation_exceeds_published_code_even_if_clock_is_behind(self):
        entries = [{"body": "Android versionCode: `500000000`"}]
        with patch.object(release.time, "time", return_value=1577836801):
            self.assertEqual(release.next_version_code(entries), 500000001)

    def test_rc_publication_builds_target_version_and_publishes_immediately(self):
        sha = "a" * 40
        alpha = {"tag_name": "26.10.09-alpha1", "draft": False,
                 "body": release.release_body("owner/repo", "26.10.09-alpha1", sha, 200000000,
                                             "# New Features\n\nNo new features in this release.")}
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            self.fixture(directory)
            with patch.object(release, "releases", return_value=[alpha]), patch.object(release, "publish") as publication:
                release.candidate("owner/repo", "26.10.09-RC1", sha, 200000001, directory, predecessor=alpha)
            self.assertTrue(publication.call_args.kwargs["publish_now"])
            self.assertEqual(release.verify_assets(directory)["version_name"], "26.10.09-RC1")

    def test_promotion_rejects_nonincreasing_version_code(self):
        prior = {"tag_name": "26.10.09-RC1", "draft": False, "body": "Source commit: `" + "a" * 40 + "`"}
        with patch.object(release, "api", return_value=prior), patch.object(release, "download"), \
                patch.object(release, "verify_assets", return_value={"version_code": 200000000}):
            with self.assertRaisesRegex(ValueError, "must increase"):
                release.publish_promotion("owner/repo", "26.10.09-Stable", "a" * 40, 200000000, Path("unused"), prior["tag_name"])

    def test_stable_rejects_drafts_and_non_candidates(self):
        for data in ({"draft": True, "prerelease": True}, {"draft": False, "prerelease": False}):
            with patch.object(release, "api", return_value=data), patch.object(release, "publish") as published:
                with self.assertRaises(ValueError):
                    release.promote("owner/repo", "26.10.09-alpha1")
                published.assert_not_called()
        with self.assertRaises(ValueError):
            release.promote("owner/repo", "main")

    def test_existing_published_release_must_match_bytes(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            self.fixture(directory)
            def download(repo, tag, remote):
                self.fixture(remote)
                (remote / release.ASSETS[0]).write_bytes(b"previous APK")
            with patch.object(release, "releases", return_value=[{"tag_name": "26.10.09-alpha1", "draft": False}]), \
                    patch.object(release, "api", return_value={"sha": "a" * 40}), \
                    patch.object(release, "download", side_effect=download), \
                    patch.object(release, "run") as command:
                with self.assertRaisesRegex(ValueError, "overwrite"):
                    release.publish("owner/repo", "26.10.09-alpha1", "a" * 40, directory, "notes", True)
                command.assert_not_called()

    def test_completed_rerun_verifies_and_reuses_candidate(self):
        sha = "a" * 40
        entry = {"tag_name": "26.10.09-alpha1", "draft": False,
                 "body": release.release_body("owner/repo", "26.10.09-alpha1", sha, 200000000, "# New Features\n\n- Feature.")}
        def download(repo, tag, directory):
            self.fixture(directory)
        with patch.object(release, "releases", return_value=[entry]), \
                patch.object(release, "api", return_value={"sha": sha}), \
                patch.object(release, "download", side_effect=download), \
                patch("builtins.print") as output:
            release.plan("owner/repo", sha)
            output.assert_any_call("existing=true")
            output.assert_any_call("tag=26.10.09-alpha1")

    def test_failed_draft_reuses_original_android_version_code(self):
        sha = "a" * 40
        entry = {"tag_name": "26.10.09-alpha1", "draft": True, "target_commitish": sha,
                 "body": release.release_body("owner/repo", "26.10.09-alpha1", sha, 200000000, "# New Features\n\n- Feature.")}
        with patch.object(release, "releases", return_value=[entry]), \
                patch.object(release, "api", return_value={"sha": sha}), \
                patch("builtins.print") as output:
            release.plan("owner/repo", sha, "26.10.09")
            output.assert_any_call("existing=false")
            output.assert_any_call("version_code=200000000")

    def test_existing_tag_must_match_commit(self):
        with patch.object(release, "releases", return_value=[{"tag_name": "26.10.09-Stable", "draft": True}]), \
                patch.object(release, "api", return_value={"sha": "b" * 40}):
            with self.assertRaisesRegex(ValueError, "different source"):
                release.publish("owner/repo", "26.10.09-Stable", "a" * 40, Path("unused"), "notes", False)


if __name__ == "__main__":
    unittest.main()
