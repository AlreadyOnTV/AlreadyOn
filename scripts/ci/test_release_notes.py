"""Changelog tests isolate Git history and never publish a release."""
import unittest
from unittest.mock import patch
import subprocess

from release_notes import candidate_changes, generated_changes, source_commits, published_changelog, release_body, render_fragments, promotion_changes


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

    def issue(self, number, label='bug', title='Bug title'):
        return {'number': number, 'title': title, 'html_url': f'https://github.com/owner/repo/issues/{number}',
                'labels': [{'name': label}], 'created_at': '2026-10-01T00:00:00Z'}

    def test_generated_notes_scope_deduplicate_and_prioritize_issues(self):
        bug = self.issue(2, title='[Bug] Fix guide')
        remaining = self.issue(3)
        high = {**self.issue(4), 'labels': [{'name': 'bug'}, {'name': 'priority:high'}]}
        excluded = {**self.issue(5), 'labels': [{'name': 'bug'}, {'name': 'duplicate'}]}
        pr = {'number': 10, 'merged_at': 'date', 'merge_commit_sha': 'a'}
        pages = [[pr, {**pr, 'number': 11, 'merge_commit_sha': 'future'}], [pr],
                 [bug, remaining, high, excluded, self.issue(6, 'enhancement'), self.issue(7, 'bug', 'Feature Request: A new option')]]
        detail = {'number': 10, 'title': 'Fix guide', 'labels': [], 'url': 'https://github.com/owner/repo/pull/10',
                  'closingIssuesReferences': [{'number': 2, 'url': bug['html_url']}]}
        with patch('release_notes.source_commits', return_value=['a', 'b']), \
             patch('release_notes.github_pages', side_effect=pages), \
             patch('release_notes.github_json', side_effect=[detail, bug]), \
             patch('release_notes.subprocess.check_output', return_value='2026-10-15T00:00:00+00:00'):
            notes = generated_changes('owner/repo', 'sha', 'baseline')
        self.assertEqual(notes.count('[Bug] Fix guide'), 1)
        self.assertIn('/issues/3', notes)
        self.assertLess(notes.index('/issues/4'), notes.index('/issues/3'))
        self.assertNotIn('/issues/5', notes)
        self.assertNotIn('/issues/6', notes)
        self.assertNotIn('/issues/7', notes)
        self.assertNotIn('Fix guide', notes.split('# Known Issues')[1])

    def test_features_and_internal_prs(self):
        detail = {'title': 'Add captions', 'url': 'https://github.com/owner/repo/pull/10',
                  'labels': [{'name': 'enhancement'}], 'closingIssuesReferences': []}
        for label, expected in [('enhancement', True), ('documentation', False), ('dependencies', False)]:
            with patch('release_notes.source_commits', return_value=['a']), \
                 patch('release_notes.github_pages', side_effect=[[{'number': 10, 'merged_at': 'date', 'merge_commit_sha': 'a'}], []]), \
                 patch('release_notes.github_json', return_value={**detail, 'labels': [{'name': label}]}), \
                 patch('release_notes.subprocess.check_output', return_value='2026-10-15T00:00:00+00:00'):
                self.assertEqual('Add captions' in generated_changes('owner/repo', 'sha', None), expected)

    def test_bug_pr_without_issue_and_api_errors_fail_closed(self):
        with patch('release_notes.source_commits', return_value=['a']), \
             patch('release_notes.github_pages', return_value=[{'number': 10, 'merged_at': 'date', 'merge_commit_sha': 'a'}]), \
             patch('release_notes.github_json', return_value={'labels': [{'name': 'bug'}], 'closingIssuesReferences': []}):
            with self.assertRaisesRegex(ValueError, 'closing bug issue'):
                generated_changes('owner/repo', 'sha', None)
        with patch('release_notes.source_commits', return_value=['a']), \
             patch('release_notes.github_pages', side_effect=subprocess.CalledProcessError(1, 'gh')):
            with self.assertRaises(subprocess.CalledProcessError):
                generated_changes('owner/repo', 'sha', None)

    def test_git_range_rejects_non_ancestor_before_querying_github(self):
        with patch('release_notes.subprocess.run', side_effect=subprocess.CalledProcessError(1, 'git')), \
             patch('release_notes.github_pages') as api:
            with self.assertRaises(subprocess.CalledProcessError):
                source_commits('sha', 'unrelated')
            api.assert_not_called()

    def test_built_candidate_snapshot_survives_later_issue_changes(self):
        body = release_body('owner/repo', '26.10.18-alpha1', 'a' * 40, 23, render_fragments([]),
                            changelog_baseline='26.10.09-rc3')
        entry = {'tag_name': '26.10.18-alpha1', 'body': body}
        with patch('release_notes.generated_changes') as generate:
            self.assertEqual(candidate_changes('owner/repo', [entry], entry['tag_name'], 'a' * 40,
                                               '26.10.09-rc3'), render_fragments([]))
            generate.assert_not_called()
            with self.assertRaises(ValueError):
                candidate_changes('owner/repo', [entry], entry['tag_name'], 'b' * 40, '26.10.09-rc3')
            with self.assertRaises(ValueError):
                candidate_changes('owner/repo', [entry], entry['tag_name'], 'a' * 40, 'another')

    def test_new_candidate_needs_no_handwritten_draft(self):
        with patch('release_notes.generated_changes', return_value='generated') as generate:
            self.assertEqual(candidate_changes('owner/repo', [], 'tag', 'sha', 'baseline'), 'generated')
            generate.assert_called_once_with('owner/repo', 'sha', 'baseline')
