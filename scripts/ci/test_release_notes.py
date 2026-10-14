"""Changelog tests isolate Git history and never publish a release."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from release_notes import changelog, changelog_since, published_changelog, release_body, render_fragments, promotion_changes


class ReleaseNotesTests(unittest.TestCase):
    def test_categories_and_duplicate_entries(self):
        result = render_fragments(['## Features\n\n- Discover servers.\n',
                                   '## Bug fixes\n\n- Keep mute settings.\n',
                                   '## Features\n\n- Discover servers.\n'])
        self.assertEqual(result.count('- Discover servers.'), 1)
        self.assertIn('# Bugfixes', result)
        self.assertNotIn('## Improvements', result)
        with self.assertRaises(ValueError):
            render_fragments(['## Internals\n\n- A commit subject.'])

    def test_stable_preserves_tested_notes(self):
        changes = '## Features\n\n- Watch on TV.'
        candidate = release_body('owner/repo', '2026-10-09-rc3', 'a' * 40, 123, changes)
        self.assertEqual(published_changelog(candidate), render_fragments([changes]))
        promoted_rc = release_body('owner/repo', '2026-10-10-RC1', 'b' * 40, 123,
                       changes, candidate_tag='2026-10-10-alpha1')
        self.assertNotIn('Preview release', promoted_rc)
        stable = release_body('owner/repo', '2026-10-09-stable', 'a' * 40, 123,
                              published_changelog(candidate), '2026-10-09-rc3')
        self.assertNotIn('Stable release', stable)
        self.assertEqual(published_changelog(stable), render_fragments([changes]))
        self.assertNotIn('Build details and verification', stable)
        self.assertTrue(stable.startswith('# Download Now\n\n'))
        self.assertIn('/2026-10-09-stable/alreadyon-android.apk', stable)
        self.assertIn('/2026-10-09-stable/alreadyon-tv.apk', stable)
        self.assertNotIn('/2026-10-09-rc3/', stable)
        self.assertNotIn('Download Now', published_changelog(stable))
        self.assertEqual([line for line in stable.splitlines() if line.startswith('# ')],
                         ['# Download Now', '# New Features', '# Bugfixes', '# Known Issues'])
        self.assertIn('<!--\nSource commit:', stable)
        self.assertIn('Source commit: `' + 'a' * 40 + '`', stable)
        with self.assertRaises(ValueError):
            published_changelog('Technical notes only.')

    def test_candidate_changelog_records_its_explicit_baseline(self):
        body = release_body('owner/repo', '2026-10-10-beta', 'a' * 40, 123,
                            '## Features\n\n- Enter a server address immediately.',
                            changelog_baseline='2026-10-09-rc3')
        self.assertIn('Changelog baseline: `2026-10-09-rc3`', body)

    def test_three_sections_and_empty_placeholders(self):
        result = render_fragments([])
        self.assertEqual([line for line in result.splitlines() if line.startswith('# ')],
                         ['# New Features', '# Bugfixes', '# Known Issues'])
        self.assertIn('No new features in this release.', result)
        self.assertIn('No bugfixes in this release.', result)
        self.assertEqual(render_fragments([result]), result)

    def test_bug_and_known_issue_links_are_required(self):
        for section in ['Bugfixes', 'Known Issues']:
            with self.assertRaisesRegex(ValueError, 'GitHub issue'):
                release_body('owner/repo', '2026-10-18-alpha2', 'a' * 40, 123,
                             f'# {section}\n\n- An untracked fix.')
        release_body('owner/repo', '2026-10-18-alpha2', 'a' * 40, 123,
                     '# Bugfixes\n\n- [Issue title](https://github.com/owner/repo/issues/1)')

    def test_identical_artifact_promotion_retains_only_known_issues(self):
        body = release_body('owner/repo', '2026-10-18-alpha2', 'a' * 40, 123,
                            '# New Features\n\n- Watch TV.\n\n# Known Issues\n\n'
                            '- [Remaining bug](https://github.com/owner/repo/issues/2)')
        result = promotion_changes(body)
        self.assertNotIn('Watch TV.', result)
        self.assertIn('No new features in this release.', result)
        self.assertIn('[Remaining bug]', result)

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
            (root / 'docs/release-known-issues.md').write_text(
                '# Known Issues\n\n- [Known bug](https://github.com/owner/repo/issues/2)\n')
            git('add', '.')
            git('commit', '-qm', 'New fix')
            sha = git('rev-parse', 'HEAD')
            baseline = git('rev-parse', '2026-10-08-stable')
            # A dirty working tree cannot leak unreleased changes into this APK's notes.
            (directory / 'second.md').write_text('## Features\n\n- Future feature.\n')
            (root / 'docs/release-known-issues.md').write_text('# Known Issues\n\n- Future bug.\n')
            previous = os.getcwd()
            try:
                os.chdir(root)
                result = changelog(sha, ['2026-10-08-stable'])
                self.assertIn('New fix.', result)
                self.assertNotIn('Old feature.', result)
                self.assertNotIn('Future feature.', result)
                candidate = changelog_since(sha, baseline)
                self.assertIn('New fix.', candidate)
                self.assertIn('[Known bug]', candidate)
                self.assertNotIn('Future bug.', candidate)
                self.assertNotIn('Old feature.', candidate)
                self.assertIn('Server-only fix.', candidate)
                self.assertNotIn('Future feature.', candidate)
                with self.assertRaisesRegex(ValueError, 'ancestor'):
                    changelog_since(baseline, sha)
                self.assertIn('Old feature.', changelog(sha, []))
                self.assertIn('No new features in this release.', changelog('2026-10-08-stable', ['2026-10-08-stable']))
            finally:
                os.chdir(previous)
