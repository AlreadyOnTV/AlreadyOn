"""Generate source-scoped product notes from GitHub PRs/issues; preserve built snapshots."""
import re
import json
import subprocess

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


def github_json(*args):
    """Read metadata through the workflow token; errors must stop publication."""
    return json.loads(subprocess.check_output(['gh', *args], text=True))


def github_pages(endpoint):
    pages = github_json('api', '--paginate', '--slurp', endpoint)
    return [item for page in pages for item in page]


def labels(item):
    return {label['name'].lower() for label in item.get('labels', [])}


def kind(item):
    names = labels(item)
    title = item.get('title', '').lower()
    if re.match(r'(?:\[feature\]|feature(?: request)?:|feat(?:\([^)]*\))?:)', title):
        return 'New Features'
    if names & {'bug', 'type:bug'} or re.match(r'(?:\[bug\]|bug:|fix(?:\([^)]*\))?:)', title):
        return 'Bugfixes'
    if names & {'feature', 'enhancement', 'type:feature'} or re.match(r'(?:\[feature\]|feature:|feat(?:\([^)]*\))?:)', title):
        return 'New Features'
    return None


def issue_line(issue):
    # Preserve exact issue titles while keeping each generated entry on one line.
    title = ' '.join(issue['title'].split())
    return f"- [{title}]({issue['html_url']})"


def priority(issue):
    names = labels(issue)
    for rank, aliases in enumerate([{'p0', 'priority:critical', 'critical'},
                                    {'p1', 'priority:high', 'high'},
                                    {'p2', 'priority:medium', 'medium'}]):
        if names & aliases:
            return rank, issue['number']
    return 3, issue['number']


def source_commits(sha, baseline_tag):
    """Use Git ancestry, not merge dates, to exclude future and unrelated work."""
    if baseline_tag:
        subprocess.run(['git', 'merge-base', '--is-ancestor', baseline_tag, sha], check=True)
    revision = f'{baseline_tag}..{sha}' if baseline_tag else sha
    return subprocess.check_output(['git', 'rev-list', '--reverse', revision], text=True).splitlines()


def generated_changes(repo, sha, baseline_tag):
    commits = source_commits(sha, baseline_tag)
    commit_set = set(commits)
    prs = {}
    for commit in commits:
        for pr in github_pages(f'repos/{repo}/commits/{commit}/pulls?per_page=100'):
            if pr.get('merged_at') and pr.get('merge_commit_sha') in commit_set:
                prs[pr['number']] = pr
    groups = {section: [] for section in SECTIONS}
    fixed = set()
    emitted = set()
    for number in sorted(prs):
        pr = github_json('pr', 'view', str(number), '--repo', repo,
                         '--json', 'number,title,url,labels,closingIssuesReferences')
        # Internal/dependency PRs never become product claims just because they merged.
        if labels(pr) & {'dependencies', 'documentation', 'release:skip', 'chore', 'ci'}:
            continue
        linked_features = False
        linked_bug = False
        for ref in pr.get('closingIssuesReferences', []):
            # Cross-repository issues cannot be fetched using a local issue number.
            url = ref['url']
            if not re.fullmatch(r'https://github\.com/' + re.escape(repo) + r'/issues/\d+', url):
                continue
            issue = github_json('api', f"repos/{repo}/issues/{ref['number']}")
            section = kind(issue)
            if section == 'Bugfixes':
                fixed.add(issue['number'])
                linked_bug = True
            if section == 'New Features':
                linked_features = True
            if section and issue['number'] not in emitted:
                groups[section].append(issue_line(issue))
                emitted.add(issue['number'])
        section = kind(pr)
        if section == 'Bugfixes' and not linked_bug:
            raise ValueError(f'Bugfix PR #{number} must link a closing bug issue for release notes')
        if section == 'New Features' and not linked_features:
            groups[section].append(f"- [{' '.join(pr['title'].split())}]({pr['url']})")
    issues = github_pages(f'repos/{repo}/issues?state=open&per_page=100')
    for issue in sorted(issues, key=priority):
        if 'pull_request' in issue or kind(issue) != 'Bugfixes' or issue['number'] in fixed:
            continue
        if labels(issue) & {'duplicate', 'invalid', 'wontfix', 'release:skip'}:
            continue
        groups['Known Issues'].append(issue_line(issue))
    return render_fragments(['# ' + section + '\n\n' + '\n'.join(lines)
                             for section, lines in groups.items()])


def candidate_changes(repo, entries, tag, sha, baseline_tag):
    """Freeze metadata at the built candidate so later issue edits cannot alter reruns."""
    entry = next((entry for entry in entries if entry['tag_name'] == tag), None)
    body = (entry or {}).get('body') or ''
    if START in body:
        if re.findall(r'Source commit: `([0-9a-f]{40})`', body) != [sha]:
            raise ValueError('Existing candidate notes belong to another source')
        baseline = re.findall(r'Changelog baseline: `([^`]+)`', body)
        if baseline != ([baseline_tag] if baseline_tag else []):
            raise ValueError('Existing candidate notes belong to another predecessor')
        return render_fragments([published_changelog(body)])
    return generated_changes(repo, sha, baseline_tag)


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
