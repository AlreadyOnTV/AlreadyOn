"""Render reviewed release changes and the source snapshot of known issues."""
import re
import subprocess

SECTIONS = ('New Features', 'Bugfixes', 'Known Issues')
ALIASES = {'Features': 'New Features', 'Improvements': 'New Features', 'Bug fixes': 'Bugfixes'}
EMPTY = {'New Features': 'No new features in this release.',
         'Bugfixes': 'No bugfixes in this release.',
         'Known Issues': 'No known issues are currently listed for this release.'}
KNOWN_ISSUES = 'docs/release-known-issues.md'
START = '<!-- end-user-changelog:start -->'
END = '<!-- end-user-changelog:end -->'
DIRECTORY = 'docs/release-notes/'
ANDROID_RELEASE_MARKER = '<!-- android-release -->'


def git(*args):
    return subprocess.check_output(['git', *args], text=True).strip()


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


def known_issues(sha):
    """Read the reviewed list at the artifact source, never from a dirty checkout."""
    if not git('ls-tree', '--name-only', sha, '--', KNOWN_ISSUES):
        return ''
    return git('show', f'{sha}:{KNOWN_ISSUES}')


def promotion_changes(body):
    """Identical promoted artifacts add no changes; retain their reviewed known issues."""
    notes = render_fragments([published_changelog(body)])
    known = notes.split('# Known Issues\n\n', 1)[1]
    return render_fragments(['# Known Issues\n\n' + known])


def changelog(sha, stable_tags):
    """Read notes at the release commit; unrelated branches cannot alter the baseline."""
    bases = []
    for tag in stable_tags:
        if not re.fullmatch(r'\d{2}\.\d{2}\.\d{2}-[Ss]table', tag):
            continue
        result = subprocess.run(['git', 'merge-base', '--is-ancestor', tag, sha],
                                capture_output=True)
        if result.returncode == 0:
            bases.append((int(git('rev-list', '--count', f'{tag}..{sha}')), tag))
        elif result.returncode != 1:
            raise ValueError(f'Cannot resolve stable release baseline {tag}')
    if bases:
        base = min(bases)[1]
        files = git('diff', '--name-only', '--diff-filter=AM', base, sha, '--', DIRECTORY).splitlines()
    else:
        files = git('ls-tree', '-r', '--name-only', sha, '--', DIRECTORY).splitlines()
    fragments = [git('show', f'{sha}:{name}') for name in sorted(files) if name.endswith('.md')]
    return render_fragments(fragments + [known_issues(sha)])


def changelog_since(sha, baseline_sha):
    """Render product notes changed since the previous published release source."""
    result = subprocess.run(['git', 'merge-base', '--is-ancestor', baseline_sha, sha],
                            capture_output=True)
    if result.returncode == 1:
        raise ValueError('Changelog baseline must be an ancestor of the release source')
    if result.returncode != 0:
        raise subprocess.CalledProcessError(result.returncode, result.args)
    files = git('diff', '--name-only', '--diff-filter=AM', baseline_sha, sha,
                '--', DIRECTORY).splitlines()
    fragments = []
    for name in sorted(files):
        if not name.endswith('.md'):
            continue
        fragment = git('show', f'{sha}:{name}')
        fragments.append(fragment)
    return render_fragments(fragments + [known_issues(sha)])


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
