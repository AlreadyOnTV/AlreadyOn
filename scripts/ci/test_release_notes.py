"""Changelog tests isolate Git history and never publish a release."""
import unittest

from release_notes import reviewed_changes, published_changelog, release_body, render_fragments, promotion_changes


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
        candidate = release_body('owner/repo', '26.10.09-rc3', 'a' * 40, 123, changes)
        self.assertEqual(published_changelog(candidate), render_fragments([changes]))
        promoted_rc = release_body('owner/repo', '26.10.10-RC1', 'b' * 40, 123,
                       changes, candidate_tag='26.10.10-alpha1')
        self.assertNotIn('Preview release', promoted_rc)
        stable = release_body('owner/repo', '26.10.09-stable', 'a' * 40, 123,
                              published_changelog(candidate), '26.10.09-rc3')
        self.assertNotIn('Stable release', stable)
        self.assertEqual(published_changelog(stable), render_fragments([changes]))
        self.assertNotIn('Build details and verification', stable)
        self.assertTrue(stable.startswith('# Download Now\n\n'))
        self.assertIn('/26.10.09-stable/alreadyon-android.apk', stable)
        self.assertIn('/26.10.09-stable/alreadyon-tv.apk', stable)
        self.assertNotIn('/26.10.09-rc3/', stable)
        self.assertNotIn('Download Now', published_changelog(stable))
        self.assertEqual([line for line in stable.splitlines() if line.startswith('# ')],
                         ['# Download Now', '# New Features', '# Bugfixes', '# Known Issues'])
        self.assertIn('<!--\nSource commit:', stable)
        self.assertIn('Source commit: `' + 'a' * 40 + '`', stable)
        with self.assertRaises(ValueError):
            published_changelog('Technical notes only.')

    def test_candidate_changelog_records_its_explicit_baseline(self):
        body = release_body('owner/repo', '26.10.10-beta', 'a' * 40, 123,
                            '## Features\n\n- Enter a server address immediately.',
                            changelog_baseline='26.10.09-rc3')
        self.assertIn('Changelog baseline: `26.10.09-rc3`', body)

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
                release_body('owner/repo', '26.10.18-alpha2', 'a' * 40, 123,
                             f'# {section}\n\n- An untracked fix.')
        release_body('owner/repo', '26.10.18-alpha2', 'a' * 40, 123,
                     '# Bugfixes\n\n- [Issue title](https://github.com/owner/repo/issues/1)')
        described = release_body(
            'owner/repo', '26.10.18-alpha2', 'a' * 40, 123,
            '# Bugfixes\n\n- [[Bug][TV] Fix guide focus](https://github.com/owner/repo/issues/62): Restore focus after returning to the guide.')
        self.assertIn(
            '- [[Bug][TV] Fix guide focus](https://github.com/owner/repo/issues/62): Restore focus after returning to the guide.',
            described,
        )

    def test_identical_artifact_promotion_retains_only_known_issues(self):
        body = release_body('owner/repo', '26.10.18-alpha2', 'a' * 40, 123,
                            '# New Features\n\n- Watch TV.\n\n# Known Issues\n\n'
                            '- [Remaining bug](https://github.com/owner/repo/issues/2)')
        result = promotion_changes(body)
        self.assertNotIn('Watch TV.', result)
        self.assertIn('No new features in this release.', result)
        self.assertIn('[Remaining bug]', result)

    def draft(self, sha='a' * 40, baseline='26.10.09-rc3'):
        return {'tag_name': '26.10.18-alpha1', 'target_commitish': sha, 'draft': True,
                'body': '# New Features\n\n- Watch TV.\n\n# Bugfixes\n\nNo bugfixes in this release.\n\n'
                        '# Known Issues\n\n- [Remaining bug](https://github.com/owner/repo/issues/2)\n\n'
                        f'<!--\nNotes source commit: `{sha}`\nNotes baseline: `{baseline}`\n-->\n'}

    def test_reviewed_draft_is_bound_to_source_and_predecessor(self):
        draft = self.draft()
        result = reviewed_changes([draft], draft['tag_name'], 'a' * 40, '26.10.09-rc3')
        self.assertIn('Watch TV.', result)
        self.assertIn('[Remaining bug]', result)
        self.assertNotIn('Notes source commit', result)
        for sha, baseline in [('b' * 40, '26.10.09-rc3'), ('a' * 40, '26.10.09-rc2')]:
            with self.assertRaises(ValueError):
                reviewed_changes([draft], draft['tag_name'], sha, baseline)
        with self.assertRaisesRegex(ValueError, 'Prepare reviewed notes'):
            reviewed_changes([], draft['tag_name'], 'a' * 40, None)

    def test_first_release_and_invalid_draft_cannot_bypass_review(self):
        draft = self.draft(baseline='none')
        self.assertIn('Watch TV.', reviewed_changes([draft], draft['tag_name'], 'a' * 40, None))
        for patch in [{'target_commitish': '26.10.18'}, {'draft': False}, {'assets': [{'name': 'apk'}]},
                      {'body': 'Unstructured notes'}, {'body': draft['body'].replace('# Known Issues', '# Internals')}]:
            with self.assertRaises(ValueError):
                reviewed_changes([{**draft, **patch}], draft['tag_name'], 'a' * 40, None)

    def test_published_rerun_reads_existing_notes(self):
        entry = {'tag_name': '26.10.18-alpha1', 'draft': False,
                 'body': release_body('owner/repo', '26.10.18-alpha1', 'a' * 40, 23,
                                      render_fragments([]), changelog_baseline='26.10.09-rc3')}
        self.assertEqual(reviewed_changes([entry], entry['tag_name'], 'a' * 40, '26.10.09-rc3'),
                         render_fragments([]))
