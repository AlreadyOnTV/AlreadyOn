"""Changelog tests isolate Git history and never publish a release."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from release_notes import changelog, changelog_since, published_changelog, release_body, render_fragments


class ReleaseNotesTests(unittest.TestCase):
    def test_categories_and_duplicate_entries(self):
        result = render_fragments(['## Features\n\n- Discover servers.\n',
                                   '## Bug fixes\n\n- Keep mute settings.\n',
                                   '## Features\n\n- Discover servers.\n'])
        self.assertEqual(result.count('- Discover servers.'), 1)
        self.assertIn('## Bug fixes', result)
        self.assertNotIn('## Improvements', result)
        with self.assertRaises(ValueError):
            render_fragments(['## Internals\n\n- A commit subject.'])

    def test_stable_preserves_tested_notes(self):
        changes = '## Features\n\n- Watch on TV.'
        candidate = release_body('owner/repo', '2026-10-09-rc3', 'a' * 40, 123, changes)
        self.assertTrue(candidate.startswith('Preview release'))
        promoted_rc = release_body('owner/repo', '2026-10-10-RC1', 'b' * 40, 123,
                       changes, candidate_tag='2026-10-10-alpha1')
        self.assertTrue(promoted_rc.startswith('Preview release'))
        stable = release_body('owner/repo', '2026-10-09-stable', 'a' * 40, 123,
                              published_changelog(candidate), '2026-10-09-rc3')
        self.assertTrue(stable.startswith('Stable release'))
        self.assertEqual(published_changelog(stable), changes)
        self.assertIn('/2026-10-09-stable/alreadyon-tv.apk', stable)
        self.assertIn('Source commit: `' + 'a' * 40 + '`', stable)
        with self.assertRaises(ValueError):
            published_changelog('Technical notes only.')

    def test_candidate_changelog_records_its_explicit_baseline(self):
        body = release_body('owner/repo', '2026-10-10-beta', 'a' * 40, 123,
                            '## Features\n\n- Enter a server address immediately.',
                            changelog_baseline='2026-10-09-rc3')
        self.assertIn('Changelog baseline: `2026-10-09-rc3`', body)

    def test_git_range_uses_reachable_stable_and_release_snapshot(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            def git(*args):
                return subprocess.check_output(['git', *args], cwd=root, text=True).strip()
            git('init', '-q')
            git('config', 'user.name', 'Synthetic test')
            git('config', 'user.email', 'test@example.invalid')
            directory = root / 'docs/release-notes'
            directory.mkdir(parents=True)
            (directory / 'first.md').write_text('## Features\n\n- Old feature.\n')
            git('add', '.')
            git('commit', '-qm', 'First feature')
            git('tag', '2026-10-08-stable')
            (directory / 'second.md').write_text('<!-- android-release -->\n\n## Bug fixes\n\n- New fix.\n')
            (directory / 'server.md').write_text('## Bug fixes\n\n- Server-only fix.\n')
            git('add', '.')
            git('commit', '-qm', 'New fix')
            sha = git('rev-parse', 'HEAD')
            baseline = git('rev-parse', '2026-10-08-stable')
            # A dirty working tree cannot leak unreleased changes into this APK's notes.
            (directory / 'second.md').write_text('## Features\n\n- Future feature.\n')
            previous = os.getcwd()
            try:
                os.chdir(root)
                result = changelog(sha, ['2026-10-08-stable'])
                self.assertIn('New fix.', result)
                self.assertNotIn('Old feature.', result)
                self.assertNotIn('Future feature.', result)
                candidate = changelog_since(sha, baseline)
                self.assertIn('New fix.', candidate)
                self.assertNotIn('Old feature.', candidate)
                self.assertNotIn('Server-only fix.', candidate)
                self.assertNotIn('Future feature.', candidate)
                with self.assertRaisesRegex(ValueError, 'ancestor'):
                    changelog_since(baseline, sha)
                self.assertIn('Old feature.', changelog(sha, []))
                self.assertIn('No new user-facing changes', changelog('2026-10-08-stable', ['2026-10-08-stable']))
            finally:
                os.chdir(previous)
