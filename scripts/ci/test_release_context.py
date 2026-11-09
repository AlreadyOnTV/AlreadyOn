"""Current-date release context and immutable source-tag tests."""
import json
from datetime import datetime, timezone
from importlib.util import module_from_spec, spec_from_file_location
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

SPEC = spec_from_file_location("release_context", Path(__file__).with_name("release-context.py"))
release_context = module_from_spec(SPEC)
SPEC.loader.exec_module(release_context)


class ReleaseContextTests(unittest.TestCase):
    def test_current_date_uses_new_york_calendar_day(self):
        instant = datetime.fromisoformat("2026-11-08T04:30:00+00:00")
        self.assertEqual(release_context.resolve_date("a" * 40, now=instant), "26.11.07")

    def test_explicit_date_is_validated(self):
        self.assertEqual(release_context.resolve_date("a" * 40, "26.11.08"), "26.11.08")
        with self.assertRaisesRegex(ValueError, "valid YY.MM.DD"):
            release_context.resolve_date("a" * 40, "26.02.30")
        with self.assertRaisesRegex(ValueError, "full source commit"):
            release_context.resolve_date("short", "26.11.08")

    def test_source_tag_is_created_once_when_absent(self):
        sha = "a" * 40
        with patch.object(release_context, "remote_ref", return_value=None), \
                patch.object(release_context.subprocess, "run") as command:
            release_context.update_source_tag("owner/repo", "26.11.08", sha)
        payload = json.loads(command.call_args.kwargs["input"])
        self.assertEqual(payload, {"ref": "refs/tags/26.11.08", "sha": sha})

    def test_existing_source_tag_advances_to_validated_source(self):
        with patch.object(release_context, "remote_ref", return_value="b" * 40), \
                patch.object(release_context.subprocess, "run") as command:
            release_context.update_source_tag("owner/repo", "26.11.08", "a" * 40)
        payload = json.loads(command.call_args.kwargs["input"])
        self.assertEqual(payload, {"sha": "a" * 40, "force": True})
        self.assertIn("PATCH", command.call_args.args[0])

    def test_missing_ref_detects_github_404(self):
        result = subprocess.CompletedProcess(
            ["gh", "api"], 1, '{"message":"Not Found","status":"404"}', "gh: Not Found")
        with patch.object(release_context.subprocess, "run", return_value=result):
            self.assertIsNone(release_context.remote_ref("owner/repo", "tags/26.11.08"))


if __name__ == "__main__":
    unittest.main()
