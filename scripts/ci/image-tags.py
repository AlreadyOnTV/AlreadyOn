#!/usr/bin/env python3
"""Map trusted branches to channel/date tags; resolve promotion sources by digest."""
import datetime
import os
import re
from zoneinfo import ZoneInfo

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


def image_tags(repository, branch, revision, now=None, release_date=None):
    """Dev builds get channel/date; promotions add only their channel."""
    sha(revision)
    channel = {'dev': 'nightly', 'test': 'latest', 'main': 'stable'}[branch]
    now = now or datetime.datetime.now(datetime.timezone.utc)
    day = release_date or now.astimezone(ZoneInfo('America/New_York')).strftime('%Y-%m-%d')
    if not re.fullmatch(r'\d{4}-\d{2}-\d{2}', day):
        raise ValueError('Release date must use YYYY-MM-DD')
    try:
        datetime.datetime.strptime(day, '%Y-%m-%d')
    except ValueError as error:
        raise ValueError('Release date must be a valid YYYY-MM-DD date') from error
    image = 'ghcr.io/' + repository.lower()
    tags = [f'{image}:{channel}']
    if branch == 'dev':
        tags.append(f'{image}:{day}')
    return tags


if __name__ == '__main__':
    repository = os.environ['GITHUB_REPOSITORY']
    branch = os.environ.get('CI_SOURCE_BRANCH') or os.environ['GITHUB_REF_NAME']
    revision = os.environ.get('CI_REVISION') or os.environ['GITHUB_SHA']
    release_date = os.environ.get('CI_RELEASE_DATE')
    print('tags=' + ' '.join(image_tags(repository, branch, revision, release_date=release_date)))
    if os.environ.get('CI_PR_NUMBER'):
        print(f"source=ghcr.io/{repository.lower()}:pr-{int(os.environ['CI_PR_NUMBER'])}")
    elif os.environ.get('CI_SELECTED_SHA'):
        print('source=' + source_tag(repository, os.environ['CI_SELECTED_SHA']))
