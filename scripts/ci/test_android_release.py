"""Focused release tests for the two-stage Nightly/Stable lifecycle."""
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("android_release", Path(__file__).with_name("android-release.py"))
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)
SHA = "a" * 40
OTHER = "b" * 40
DIGEST = "ghcr.io/owner/repo@sha256:" + "d" * 64


class AndroidReleaseTests(unittest.TestCase):
    def assets(self, directory, tag="26.11.08-nightly", sha=SHA, code=200000100):
        phone, tv = directory / release.ASSETS[0], directory / release.ASSETS[3]
        phone.write_bytes(b"synthetic phone APK")
        tv.write_bytes(b"synthetic TV APK")
        manifest = {"source_sha": sha, "version_name": tag, "version_code": code,
                    "package": "io.github.alreadyonapp.alreadyon",
                    "apk_sha256": release.digest(phone), "tv_apk_sha256": release.digest(tv),
                    "artifacts": {
                        "phone": {"file": release.ASSETS[0], "sha256": release.digest(phone)},
                        "tv": {"file": release.ASSETS[3], "sha256": release.digest(tv)}}}
        (directory / release.ASSETS[2]).write_text(json.dumps(manifest) + "\n")
        (directory / release.ASSETS[1]).write_text("".join(
            f"{release.digest(directory / name)}  {name}\n"
            for name in (release.ASSETS[0], release.ASSETS[2], release.ASSETS[3])))

    @staticmethod
    def entries(directory):
        return [{"name": name} for name in release.release_assets(directory)]

    @staticmethod
    def copy_assets(source):
        def download(_repo, _tag, destination):
            for name in release.release_assets(source):
                shutil.copyfile(source / name, destination / name)
        return download

    def test_manifest_binds_exact_source_phone_tv_and_checksums(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            self.assets(directory)
            result = release.verify_assets(directory, SHA)
            self.assertEqual(result["source_sha"], SHA)
            self.assertEqual(result["artifacts"]["phone"]["file"], release.ASSETS[0])
            self.assertEqual(result["artifacts"]["tv"]["file"], release.ASSETS[3])
            with self.assertRaisesRegex(ValueError, "source revision"):
                release.verify_assets(directory, OTHER)
            (directory / release.ASSETS[3]).write_bytes(b"tampered TV APK")
            with self.assertRaisesRegex(ValueError, "TV APK checksum"):
                release.verify_assets(directory, SHA)

    def test_candidate_creation_rejects_retired_stages(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            directory.mkdir(exist_ok=True)
            for tag in ("26.11.08-alpha1", "26.11.08-rc1", "26.11.08-beta1"):
                with self.subTest(tag=tag):
                    with self.assertRaisesRegex(ValueError, "Invalid Android release"):
                        release.candidate("owner/repo", tag, SHA, 200000100, directory)

    def test_nightly_same_source_rerun_reuses_verified_assets(self):
        entry = {"tag_name": "26.11.08-nightly", "draft": False, "prerelease": True,
                 "body": f"Source commit: `{SHA}`\nAndroid versionCode: `200000100`"}
        with patch.object(release, "releases", return_value=[entry]), \
                patch.object(release, "api", return_value={"sha": SHA}), \
                patch.object(release, "download"), \
                patch.object(release, "verify_assets", return_value={"version_code": 200000100}), \
                patch.object(release, "require_tested_image"), \
                patch("builtins.print") as output:
            release.plan_nightly("owner/repo", SHA, "26.11.08")
            output.assert_any_call("existing=true")
            output.assert_any_call("version_code=200000100")

    def test_nightly_collision_allocates_higher_code_for_new_source(self):
        old = {"tag_name": "26.11.08-nightly", "draft": False, "prerelease": True,
               "body": f"Source commit: `{OTHER}`\nAndroid versionCode: `200000100`"}
        with patch.object(release, "releases", return_value=[old]), \
                patch.object(release, "next_version_code", return_value=200000101), \
                patch("builtins.print") as output:
            release.plan_nightly("owner/repo", SHA, "26.11.08")
            output.assert_any_call("existing=false")
            output.assert_any_call("version_code=200000101")

    def test_nightly_replacement_regenerates_notes_for_new_source(self):
        tag = "26.11.09-nightly"
        old = {"tag_name": tag, "draft": False, "prerelease": True,
               "body": f"Source commit: `{OTHER}`\nAndroid versionCode: `200000100`"}
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            self.assets(directory, tag=tag, sha=SHA, code=200000101)
            with patch.object(release, "require_tested_image"), \
                    patch.object(release, "releases", return_value=[old]), \
                    patch.object(release, "changelog_baseline", return_value="26.11.08-nightly"), \
                    patch.object(release, "candidate_changes", return_value="replacement notes") as notes, \
                    patch.object(release, "release_body", return_value="replacement body"), \
                    patch.object(release, "publish") as publish:
                release.candidate("owner/repo", tag, SHA, 200000101, directory, nightly=True)
            notes.assert_called_once_with("owner/repo", [old], tag, SHA, "26.11.08-nightly",
                                          allow_source_replacement=True)
            self.assertTrue(publish.call_args.kwargs["replace"])

    def test_latest_nightly_selects_newest_source_in_main_history(self):
        entries = [
            {"tag_name": "26.11.06-nightly", "draft": False, "prerelease": True,
             "published_at": "2026-11-06T00:00:00Z", "body": f"Source commit: `{SHA}`"},
            {"tag_name": "26.11.08-nightly", "draft": False, "prerelease": True,
             "published_at": "2026-11-08T00:00:00Z", "body": f"Source commit: `{OTHER}`"},
            {"tag_name": "26.11.09-nightly", "draft": False, "prerelease": True,
             "published_at": "2026-11-09T00:00:00Z", "body": f"Source commit: `{'c' * 40}`"},
        ]
        def ancestry(args, **_kwargs):
            return subprocess.CompletedProcess(args, 1 if args[-2] == "c" * 40 else 0)
        def tag_sha(path):
            return {"sha": {"26.11.06-nightly": SHA,
                            "26.11.08-nightly": OTHER}[path.split("/")[-1]]}
        with patch.object(release, "releases", return_value=entries), \
                patch.object(release, "api", side_effect=tag_sha), \
                patch.object(release.subprocess, "run", side_effect=ancestry), \
                patch.object(release, "download"), \
                patch.object(release, "verify_assets"), \
                patch.object(release, "require_tested_image"):
            entry, source = release.latest_nightly("owner/repo", "a" * 40)
        self.assertEqual(entry["tag_name"], "26.11.08-nightly")
        self.assertEqual(source, OTHER)

    def test_stable_rebuild_uses_nightly_source_and_increases_code(self):
        nightly = {"tag_name": "26.11.08-nightly", "draft": False, "prerelease": True,
                   "body": f"Source commit: `{SHA}`\nAndroid versionCode: `200000100`"}
        prior_stable = {"tag_name": "26.11.08-stable", "draft": False, "prerelease": False,
                        "body": f"Source commit: `{OTHER}`\nAndroid versionCode: `200000120`"}
        with patch.object(release, "require_tested_image"), \
                patch.object(release, "releases", return_value=[nightly, prior_stable]), \
                patch.object(release, "remote_tag_exists", return_value=True), \
                patch.object(release, "download"), \
                patch.object(release, "verify_assets", return_value={"version_code": 200000100}), \
                patch.object(release, "next_version_code", return_value=200000121), \
                patch("builtins.print") as output:
            release.plan_stable("owner/repo", SHA, "26.11.08", predecessor_tag=nightly["tag_name"])
        output.assert_any_call("source_sha=" + SHA)
        output.assert_any_call("predecessor=26.11.08-nightly")
        output.assert_any_call("version_code=200000121")

    def test_replacement_deletes_old_release_only_after_new_assets_exist(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            self.assets(directory)
            old = {"tag_name": "26.11.08-nightly", "draft": False,
                   "body": f"Source commit: `{OTHER}`"}
            commands = []
            with patch.object(release, "releases", return_value=[old]), \
                    patch.object(release, "run", side_effect=lambda *args: commands.append(args)), \
                    patch.object(release, "download", side_effect=self.copy_assets(directory)):
                release.publish("owner/repo", "26.11.08-nightly", SHA, directory,
                                "new notes", True, replace=True)
            delete = next(i for i, command in enumerate(commands) if command[:3] == ("gh", "release", "delete"))
            create = next(i for i, command in enumerate(commands) if command[:3] == ("gh", "release", "create"))
            self.assertLess(delete, create)
            self.assertIn("--target", commands[create])

    def test_exact_published_release_rerun_verifies_without_replacement(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            self.assets(directory)
            entry = {"tag_name": "26.11.08-nightly", "draft": False}
            with patch.object(release, "releases", return_value=[entry]), \
                    patch.object(release, "api", return_value={"sha": SHA}), \
                    patch.object(release, "download", side_effect=self.copy_assets(directory)), \
                    patch.object(release, "run") as command:
                release.publish("owner/repo", "26.11.08-nightly", SHA, directory, "notes", True)
            command.assert_not_called()


if __name__ == "__main__":
    unittest.main()
