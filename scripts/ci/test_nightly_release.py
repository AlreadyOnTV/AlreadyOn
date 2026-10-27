"""Verify immutable nightly identity and exact-source stage promotion without publishing."""
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('nightly_android_release', Path(__file__).with_name('android-release.py'))
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)
SHA = 'a' * 40


class NightlyReleaseTests(unittest.TestCase):
    def test_new_tags_and_legacy_tags_share_calendar_and_stage_order(self):
        for tag, stage in [('26.10.26-nightly', 'nightly'), ('26.10.26-rc1', 'rc'),
                           ('26.10.26-stable', 'stable'), ('26.10.25-RC1', 'rc'),
                           ('26.10.25-Stable', 'stable')]:
            self.assertEqual(release.stage_tag(tag)[1], stage)
        self.assertEqual(release.changelog_baseline([{'tag_name': '26.10.26-nightly', 'draft': False}],
                                                   '26.10.26-rc1'), '26.10.26-nightly')

    def test_docker_dated_nightly_is_an_alias_for_the_validated_source(self):
        spec = importlib.util.spec_from_file_location('nightly_image_tags', Path(__file__).with_name('image-tags.py'))
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        self.assertEqual(module.image_tags('Owner/Repo', 'dev', SHA, release_date='26.10.26', publication='nightly'),
                         ['ghcr.io/owner/repo:nightly', 'ghcr.io/owner/repo:26.10.26', 'ghcr.io/owner/repo:26.10.26-nightly'])

    def test_another_source_cannot_replace_a_dated_nightly(self):
        for draft in (True, False):
            entry = {'tag_name': '26.10.26-nightly', 'draft': draft, 'body': f'Source commit: `{"b" * 40}`'}
            with patch.object(release, 'releases', return_value=[entry]), self.assertRaisesRegex(ValueError, 'different nightly'):
                release.check_nightly('owner/repo', SHA, '26.10.26')

    def test_orphan_remote_tag_prevents_build_even_with_shallow_checkout(self):
        with patch.object(release, 'releases', return_value=[]), \
                patch.object(release, 'remote_tag_exists', return_value=True), \
                self.assertRaisesRegex(ValueError, 'without a matching release'):
            release.check_nightly('owner/repo', SHA, '26.10.26')

    def test_invalid_date_and_source_fail_before_network_access(self):
        with patch.object(release, 'releases') as releases:
            for day, sha in [('26.02.30', SHA), ('26.10.26', 'latest')]:
                with self.assertRaises(ValueError):
                    release.check_nightly('owner/repo', sha, day)
            releases.assert_not_called()

    def test_completed_nightly_verifies_remote_tag_assets_and_image(self):
        entry = {'tag_name': '26.10.26-nightly', 'draft': False, 'body': f'Source commit: `{SHA}`'}
        with patch.object(release, 'releases', return_value=[entry]), \
                patch.object(release, 'api', return_value={'sha': SHA}), \
                patch.object(release, 'download') as download, patch.object(release, 'verify_assets') as verify, \
                patch.object(release, 'require_tested_image') as image:
            self.assertEqual(release.check_nightly('owner/repo', SHA, '26.10.26'), entry)
            download.assert_called_once()
            self.assertEqual(verify.call_args.args[1], SHA)
            image.assert_called_once_with('owner/repo', SHA)

    def test_rc_plan_uses_nightly_source_and_lowercase_rc1(self):
        entry = {'tag_name': '26.10.26-nightly', 'draft': False, 'body': f'Source commit: `{SHA}`'}
        with patch.object(release, 'releases', return_value=[entry]), \
                patch.object(release, 'published_stable', return_value=None), \
                patch.object(release, 'api', return_value={'sha': SHA}), \
                patch.object(release, 'download'), patch.object(release, 'verify_assets', return_value={'version_code': 100}), \
                patch.object(release, 'require_tested_image'), patch.object(release, 'next_version_code', return_value=101), \
                patch.object(release.subprocess, 'run'), patch('builtins.print') as output:
            release.promotion_plan('owner/repo', '26.10.26', SHA, 'rc')
            output.assert_any_call('tag=26.10.26-rc1')
            output.assert_any_call('source_sha=' + SHA)
            output.assert_any_call('predecessor=26.10.26-nightly')


if __name__ == '__main__':
    unittest.main()
