#!/usr/bin/env python3
"""Plan dated Android release retention; --apply deletes expired releases and tags."""
import argparse
from datetime import datetime, timedelta
import importlib.util
import json
import os
from pathlib import Path
import subprocess
from zoneinfo import ZoneInfo

SPEC = importlib.util.spec_from_file_location('android_release', Path(__file__).with_name('android-release.py'))
android_release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(android_release)

TZ = ZoneInfo('America/New_York')
CHANNELS = {'nightly', 'stable'}


def publication_day(entry):
    published = entry.get('published_at')
    if not published:
        return None
    return datetime.fromisoformat(published.replace('Z', '+00:00')).astimezone(TZ).date()


def retention_plan(entries, today, days=90, keep=5):
    """Return release id to keep/delete reason without treating unknown tags as disposable."""
    recognized = []
    reasons = {}
    for entry in entries:
        if entry.get('draft'):
            reasons[entry['id']] = 'draft release'
            continue
        parsed = android_release.stage_tag(entry.get('tag_name', ''))
        if parsed is None:
            reasons[entry['id']] = 'unrecognized release tag'
            continue
        day, channel, _ = parsed
        published_day = publication_day(entry)
        if published_day is None:
            reasons[entry['id']] = 'missing publication date'
            continue
        expected_prerelease = channel != 'stable'
        if entry.get('prerelease') != expected_prerelease:
            reasons[entry['id']] = 'release classification mismatch'
            continue
        recognized.append((entry, day, channel, published_day))

    protected = set()
    for channel in CHANNELS:
        candidates = [item for item in recognized if item[2] == channel]
        if candidates:
            newest = max(candidates, key=lambda item: (item[3], item[1]))
            protected.add(newest[0]['id'])
            reasons[newest[0]['id']] = f'latest {channel} release'

    cutoff = today - timedelta(days=days)
    expired = [item for item in recognized if item[3] < cutoff and item[0]['id'] not in protected]
    recent_expired = {item[0]['id'] for item in
                      sorted(expired, key=lambda item: (item[3], item[1]), reverse=True)[:keep]}
    for entry, _, _, published_day in recognized:
        release_id = entry['id']
        if release_id in protected:
            continue
        if published_day >= cutoff:
            reasons[release_id] = 'published within retention window'
        elif release_id in recent_expired:
            reasons[release_id] = f'one of the {keep} newest expired releases'
        else:
            reasons[release_id] = None
    return reasons


def api(path, *args):
    result = subprocess.check_output(['gh', 'api', *args, path], text=True)
    return json.loads(result) if result.strip() else None


def releases(repository):
    pages = api(f'repos/{repository}/releases?per_page=100', '--paginate', '--slurp')
    return [entry for page in pages for entry in page]


def delete_tag(repository, tag):
    ref = subprocess.run(['gh', 'api', f'repos/{repository}/git/ref/tags/{tag}'],
                         capture_output=True, text=True)
    if ref.returncode and '404' in ref.stderr:
        return
    if ref.returncode:
        raise subprocess.CalledProcessError(ref.returncode, ref.args, ref.stdout, ref.stderr)
    subprocess.run(['gh', 'api', '-X', 'DELETE', f'repos/{repository}/git/refs/tags/{tag}'], check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    repository = os.environ['GITHUB_REPOSITORY']
    today = datetime.now(TZ).date()
    entries = releases(repository)
    plan = retention_plan(entries, today)
    deletions = []
    for entry in entries:
        reason = plan[entry['id']]
        print(f"{'KEEP' if reason else 'DELETE'} {entry['tag_name']}: {reason or 'older than 90 days and outside the five-release reserve'}")
        if reason is None:
            deletions.append((entry['id'], entry['tag_name']))

    if not args.apply:
        print('Dry run: no releases or tags deleted')
        return

    for release_id, tag in deletions:
        current_entries = releases(repository)
        current_plan = retention_plan(current_entries, today)
        current = next((entry for entry in current_entries if entry['id'] == release_id), None)
        if current is None:
            continue
        if current['tag_name'] != tag or current_plan.get(release_id) is not None:
            raise RuntimeError('Release retention changed during cleanup; refusing deletion')
        delete_tag(repository, tag)
        subprocess.run(['gh', 'api', '-X', 'DELETE', f'repos/{repository}/releases/{release_id}'], check=True)


if __name__ == '__main__':
    main()
