#!/usr/bin/env python3
"""Map trusted branches to channel/date tags; resolve promotion sources by digest."""
import datetime
import os
import re
from zoneinfo import ZoneInfo
from release_dates import release_day

SHA = re.compile(r'[0-9a-f]{40}')


def sha(value):
    """Reject anything but a full commit id so no other text can become part of a tag."""
    if not SHA.fullmatch(value or ''):
        raise ValueError('Expected a full lowercase commit SHA')
    return value


def source_tag(repository, selected):
    """Resolve a recorded release, with a legacy fallback during migration."""
    from image_registry import release_source
    return release_source(repository, sha(selected))


def image_tags(repository, branch, revision, now=None, release_date=None, publication=None,
               sync_channels=False):
    """Dev builds get channel/date; promotions add only their channel."""
    sha(revision)
    if sync_channels and branch != 'main':
        raise ValueError('All-channel synchronization is only available on main')
    channel = {'dev': 'nightly', 'test': 'latest', 'main': 'stable'}[branch]
    now = now or datetime.datetime.now(datetime.timezone.utc)
    day = release_date or now.astimezone(ZoneInfo('America/New_York')).strftime('%y.%m.%d')
    day = release_day(day)
    image = 'ghcr.io/' + repository.lower()
    if sync_channels:
        return [f'{image}:nightly', f'{image}:latest', f'{image}:stable']
    tags = [f'{image}:{channel}']
    if branch == 'dev':
        tags.append(f'{image}:{day}')
        if publication == 'nightly':
            tags.append(f'{image}:{day}-nightly')
    return tags


if __name__ == '__main__':
    repository = os.environ['GITHUB_REPOSITORY']
    branch = os.environ.get('CI_SOURCE_BRANCH') or os.environ['GITHUB_REF_NAME']
    revision = os.environ.get('CI_REVISION') or os.environ['GITHUB_SHA']
    selected = os.environ.get('CI_SELECTED_SHA') or revision
    release_date = os.environ.get('CI_RELEASE_DATE')
    sync_channels = os.environ.get('CI_CHANNEL_SYNC') == 'true'
    if branch == 'main' and not sync_channels:
        from image_registry import has_stable_reissue
        sync_channels = has_stable_reissue(repository, selected)
    print('tags=' + ' '.join(image_tags(
        repository, branch, revision, release_date=release_date,
        publication=os.environ.get('CI_PUBLICATION'), sync_channels=sync_channels)))
    if os.environ.get('CI_PR_NUMBER'):
        print(f"source=ghcr.io/{repository.lower()}:pr-{int(os.environ['CI_PR_NUMBER'])}")
    elif os.environ.get('CI_SELECTED_SHA'):
        print('source=' + source_tag(repository, os.environ['CI_SELECTED_SHA']))
