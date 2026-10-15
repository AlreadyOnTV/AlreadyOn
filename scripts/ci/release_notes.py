"""Render reviewed GitHub Release notes without repository changelog files."""
import re

SECTIONS = ('New Features', 'Bugfixes', 'Known Issues')
ALIASES = {'Features': 'New Features', 'Improvements': 'New Features', 'Bug fixes': 'Bugfixes'}
EMPTY = {'New Features': 'No new features in this release.',
         'Bugfixes': 'No bugfixes in this release.',
         'Known Issues': 'No known issues are currently listed for this release.'}
START = '<!-- end-user-changelog:start -->'
END = '<!-- end-user-changelog:end -->'
ANDROID_RELEASE_MARKER = '<!-- android-release -->'


def render_fragments(fragments):
    """Accept historical fragment headings while always emitting the public schema."""
    groups = {section: [] for section in SECTIONS}
    for fragment in fragments:
        section = None
        for line in fragment.splitlines():
            heading = re.fullmatch(r'#{1,2} (.+)', line)
            if not line.strip() or line.strip() == ANDROID_RELEASE_MARKER:
                continue
            if heading:
                section = ALIASES.get(heading[1], heading[1])
                if section not in groups:
                    raise ValueError('Release notes require supported headings')
            elif line.startswith('- ') and section:
                if line not in groups[section]:
                    groups[section].append(line)
            elif section and line == EMPTY[section]:
                continue
            else:
                raise ValueError('Release notes require supported headings and single-line bullet entries')
    return '\n\n'.join('# ' + section + '\n\n' +
                         ('\n'.join(entries) if entries else EMPTY[section])
                         for section, entries in groups.items())


def promotion_changes(body):
    """Identical promoted artifacts add no changes; retain their reviewed known issues."""
    notes = render_fragments([published_changelog(body)])
    known = notes.split('# Known Issues\n\n', 1)[1]
    return render_fragments(['# Known Issues\n\n' + known])


def reviewed_changes(entries, tag, sha, baseline_tag):
    """Require notes reviewed for this exact source/predecessor before publication.

    Notes-only drafts use separate metadata so they cannot masquerade as built
    candidates. Published reruns use the existing artifact provenance markers.
    """
    entry = next((entry for entry in entries if entry['tag_name'] == tag), None)
    if entry is None:
        raise ValueError(f'Prepare reviewed notes in the GitHub Release draft for {tag}')
    body = entry.get('body') or ''
    is_notes_draft = 'Notes source commit:' in body
    source_label = 'Notes source commit' if is_notes_draft else 'Source commit'
    baseline_label = 'Notes baseline' if is_notes_draft else 'Changelog baseline'
    source = re.findall(rf'{source_label}: `([0-9a-f]{{40}})`', body)
    baseline = re.findall(rf'{baseline_label}: `([^`]+)`', body)
    if source != [sha] or entry.get('target_commitish') != sha and entry.get('draft', False):
        raise ValueError('Reviewed release notes must target the exact source commit')
    expected = baseline_tag or 'none'
    # Older published first-release bodies omitted the absent baseline.
    if baseline != [expected] and not (not baseline and baseline_tag is None and not is_notes_draft):
        raise ValueError('Reviewed release notes do not match the predecessor release')
    if is_notes_draft:
        if not entry.get('draft', False) or entry.get('assets'):
            raise ValueError('Notes-only release drafts must be unpublished and have no assets')
        notes = body.split('<!--', 1)[0].strip()
    else:
        notes = published_changelog(body)
    headings = [line for line in notes.splitlines() if line.startswith('# ')]
    if headings != ['# New Features', '# Bugfixes', '# Known Issues']:
        raise ValueError('Reviewed notes require New Features, Bugfixes and Known Issues in order')
    return render_fragments([notes])


def published_changelog(body):
    """Stable notes come from the tested candidate, never from newer source changes."""
    if START not in body or END not in body:
        raise ValueError('Candidate has no user-facing changelog; update its release notes before stable promotion')
    notes = body.split(START, 1)[1].split(END, 1)[0].strip()
    if not notes:
        raise ValueError('Candidate user-facing changelog is empty')
    return notes


def release_body(repo, tag, sha, code, changes, candidate_tag=None, changelog_baseline=None):
    """Link this release’s APKs; hidden metadata preserves rerun/promotion identity."""
    changes = render_fragments([changes])
    section = None
    for line in changes.splitlines():
        if line.startswith('# '):
            section = line[2:]
        elif line.startswith('- ') and section in {'Bugfixes', 'Known Issues'}:
            if not re.fullmatch(r'- \[.+\]\(https://github\.com/[^/]+/[^/]+/issues/\d+\)(?:: .+)?', line):
                raise ValueError(f'{section} entries must link a GitHub issue using its title')
    # Legacy readers rely on these labels. Keep them invisible without moving or
    # replacing signed assets, and continue to verify asset provenance separately.
    metadata = f'Source commit: `{sha}`\nAndroid versionCode: `{code}`'
    if changelog_baseline:
        metadata += f'\nChangelog baseline: `{changelog_baseline}`'
    if candidate_tag:
        metadata += f'\nPromoted from: `{candidate_tag}`'
    url = f'https://github.com/{repo}/releases/download/{tag}'
    downloads = ('# Download Now\n\n'
                 f'- [Phone / tablet APK]({url}/alreadyon-android.apk)\n'
                 f'- [Google TV APK]({url}/alreadyon-tv.apk)\n\n')
    # Keep downloads outside changelog markers so promotions generate links to
    # their own tag rather than inheriting a candidate’s asset URLs.
    return downloads + START + '\n' + changes + '\n' + END + '\n\n<!--\n' + metadata + '\n-->\n'
