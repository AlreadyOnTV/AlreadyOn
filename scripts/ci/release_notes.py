"""Render reviewed user-facing changes since the nearest reachable stable release."""
import re
import subprocess

SECTIONS = ('Features', 'Improvements', 'Bug fixes')
START = '<!-- end-user-changelog:start -->'
END = '<!-- end-user-changelog:end -->'
DIRECTORY = 'docs/release-notes/'
ANDROID_RELEASE_MARKER = '<!-- android-release -->'


def git(*args):
    return subprocess.check_output(['git', *args], text=True).strip()


def render_fragments(fragments, empty_message='No new user-facing changes since the previous stable release.'):
    """Reject empty or unstructured notes rather than publishing commit-message noise."""
    groups = {section: [] for section in SECTIONS}
    for fragment in fragments:
        section = None
        for line in fragment.splitlines():
            if not line.strip():
                continue
            if line.strip() == ANDROID_RELEASE_MARKER:
                continue
            if line.startswith('## ') and line[3:] in groups:
                section = line[3:]
            elif line.startswith('- ') and section:
                if line not in groups[section]:
                    groups[section].append(line)
            else:
                raise ValueError('Release notes require supported headings and single-line bullet entries')
    body = '\n\n'.join('## ' + section + '\n\n' + '\n'.join(entries)
                         for section, entries in groups.items() if entries)
    return body or '## Changes\n\n' + empty_message


def changelog(sha, stable_tags):
    """Read notes at the release commit; unrelated branches cannot alter the baseline."""
    bases = []
    for tag in stable_tags:
        if not re.fullmatch(r'\d{4}-\d{2}-\d{2}-stable', tag):
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
    return render_fragments(fragments)


def changelog_since(sha, baseline_sha):
    """Render only release-note files changed after a tested Android release."""
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
        if ANDROID_RELEASE_MARKER in fragment.splitlines():
            fragments.append(fragment)
    return render_fragments(fragments, 'No new user-facing changes since the previous Android release.')


def published_changelog(body):
    """Stable notes come from the tested candidate, never from newer source changes."""
    if START not in body or END not in body:
        raise ValueError('Candidate has no user-facing changelog; update its release notes before stable promotion')
    notes = body.split(START, 1)[1].split(END, 1)[0].strip()
    if not notes:
        raise ValueError('Candidate user-facing changelog is empty')
    return notes


def release_body(repo, tag, sha, code, changes, candidate_tag=None, changelog_baseline=None):
    """Keep downloads and product changes prominent, with provenance collapsed below."""
    stable = tag.endswith('-Stable') or tag.endswith('-stable')
    introduction = ('Stable release for phones, tablets and Google TV.' if stable else
                    'Preview release for phones, tablets and Google TV. Please test it before relying on it for everyday viewing.')
    url = f'https://github.com/{repo}/releases/download/{tag}'
    technical = f'Source commit: `{sha}`\n\nAndroid versionCode: `{code}`'
    if changelog_baseline:
        technical += f'\n\nChangelog baseline: `{changelog_baseline}`'
    if candidate_tag:
        technical += f'\n\nPromoted from `{candidate_tag}` using the same tested APKs. Their installed version remains `{candidate_tag}`.'
    return (introduction + '\n\n' + START + '\n' + changes + '\n' + END + '\n\n'
            '## Downloads\n\n'
            f'- [Phone / tablet APK]({url}/alreadyon-android.apk)\n'
            f'- [Google TV APK]({url}/alreadyon-tv.apk)\n\n'
            '<details>\n<summary>Build details and verification</summary>\n\n'
            + technical + '\n\nChecksums and source information are attached.\n\n</details>\n')
