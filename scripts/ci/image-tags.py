#!/usr/bin/env python3
"""Map validated main sources to Nightly/date tags and explicit Stable publication to latest."""
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


def image_tags(repository, branch, revision, now=None, release_date=None, publication=None):
    """Only main publishes Nightly/date aliases or the Stable workflow's latest alias."""
    sha(revision)
    if branch != 'main':
        raise ValueError('Only main publishes release image tags')
    now = now or datetime.datetime.now(datetime.timezone.utc)
    day = release_date or now.astimezone(ZoneInfo('America/New_York')).strftime('%y.%m.%d')
    day = release_day(day)
    image = 'ghcr.io/' + repository.lower()
    if publication == 'latest':
        return [f'{image}:latest']
    if publication not in {None, 'nightly'}:
        raise ValueError('Unsupported image publication mode')
    tags = [f'{image}:nightly', f'{image}:{day}', f'{image}:{day}-nightly']
    return tags


if __name__ == '__main__':
    repository = os.environ['GITHUB_REPOSITORY']
    branch = os.environ.get('CI_SOURCE_BRANCH') or os.environ['GITHUB_REF_NAME']
    revision = os.environ.get('CI_REVISION') or os.environ['GITHUB_SHA']
    release_date = os.environ.get('CI_RELEASE_DATE')
    print('tags=' + ' '.join(image_tags(
        repository, branch, revision, release_date=release_date,
        publication=os.environ.get('CI_PUBLICATION'))))
    if os.environ.get('CI_PR_NUMBER'):
        print(f"source=ghcr.io/{repository.lower()}:pr-{int(os.environ['CI_PR_NUMBER'])}")
    elif os.environ.get('CI_SELECTED_SHA'):
        print('source=' + source_tag(repository, os.environ['CI_SELECTED_SHA']))
