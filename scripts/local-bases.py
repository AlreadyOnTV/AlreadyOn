#!/usr/bin/env python3
"""Maintain recipe-matched local bases without publishing or touching live data."""
import argparse
from datetime import datetime, timedelta, timezone
import fcntl
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('base_key', ROOT / 'scripts/ci/base-image-key.py')
keys = importlib.util.module_from_spec(spec)
spec.loader.exec_module(keys)
MAX_AGE = timedelta(days=7)


def expired(image, now):
    """Use the base build timestamp, never an inherited upstream creation label."""
    if not image:
        return True
    labels = image.get('Config', {}).get('Labels') or image.get('Labels') or {}
    stamp = labels.get('io.alreadyon.base.built-at') or image.get('Created', '')
    try:
        built = datetime.fromisoformat(stamp.replace('Z', '+00:00'))
        if built.tzinfo is None:
            return True
        age = now - built
        # A missing/invalid/future timestamp cannot establish a fresh base.
        return age < timedelta(0) or age > MAX_AGE
    except (ValueError, TypeError, AttributeError):
        return True


def inspect(engine, reference):
    """Missing images are rebuildable; engine failures must remain visible."""
    result = subprocess.run([engine, 'image', 'inspect', reference],
                            text=True, capture_output=True)
    if result.returncode:
        if any(message in result.stderr.lower() for message in
               ('no such image', 'image not known', 'not found', 'does not exist')):
            return None
        raise RuntimeError(result.stderr.strip())
    return json.loads(result.stdout)[0]


def matches(engine, image, recipe, runtime=''):
    """Check the CI parent by immutable ID, including imported digest references."""
    if not image:
        return False
    labels = image.get('Config', {}).get('Labels') or image.get('Labels') or {}
    if labels.get('io.alreadyon.base.recipe') != recipe:
        return False
    if runtime:
        parent = labels.get('io.alreadyon.base.runtime', '')
        if not parent:
            return False
        inherited = inspect(engine, parent)
        return bool(inherited and inherited['Id'] == runtime)
    return True


def ensure(engine, refresh=False):
    """Serialize base refreshes and return IDs so application consumers cannot drift."""
    refs = {}
    refreshed_runtime = False
    for kind in ('runtime', 'ci'):
        now = datetime.now(timezone.utc)
        tag = f'localhost/alreadyon-base:base-{kind}'
        recipe = keys.recipe_key(ROOT, kind)
        runtime = refs.get('runtime', '')
        current = inspect(engine, tag)
        force = refresh or (kind == 'ci' and refreshed_runtime)
        if not force and not matches(engine, current, recipe, runtime):
            cached = f'ghcr.io/alreadyontv/alreadyon-base:base-{kind}'
            imported = inspect(engine, cached)
            if matches(engine, imported, recipe, runtime) and not expired(imported, now):
                subprocess.run([engine, 'tag', imported['Id'], tag], check=True,
                               stdout=sys.stderr)
                current = imported
        force = force or expired(current, now)
        if force or not matches(engine, current, recipe, runtime):
            file = 'deploy/base.Dockerfile' if kind == 'runtime' else 'deploy/ci-base.Dockerfile'
            target = 'runtime-base' if kind == 'runtime' else 'ci-base'
            if force and runtime:
                # Pull remote toolchains separately: always-pull rejects the pinned local runtime ID.
                versions = dict(re.findall(r'^ARG (GO_VERSION|NODE_VERSION)=(\S+)$',
                                           (ROOT / file).read_text(), re.M))
                for name, version in (('golang', versions['GO_VERSION']), ('node', versions['NODE_VERSION'])):
                    subprocess.run([engine, 'pull', f'docker.io/library/{name}:{version}'],
                                   check=True, stdout=sys.stderr)
            command = [engine, 'build', '--format', 'docker', '--layers',
                       '--pull=always' if force and not runtime else '--pull=missing',
                       '--label', f'io.alreadyon.base.recipe={recipe}',
                       '--label', f'io.alreadyon.base.built-at={now.isoformat()}',
                       '--file', file, '--target', target, '--tag', tag]
            if force:
                command += ['--no-cache']
            if runtime:
                command += ['--build-arg', f'RUNTIME_BASE={runtime}',
                            '--label', f'io.alreadyon.base.runtime={runtime}']
            subprocess.run(command + ['.'], cwd=ROOT, check=True,
                           stdout=sys.stderr)
            current = inspect(engine, tag)
            if kind == 'runtime':
                refreshed_runtime = force
        if not matches(engine, current, recipe, runtime):
            raise RuntimeError(f'{tag} failed recipe/parent verification')
        refs[kind] = current['Id']
    return refs


def main():
    """Check age before builds; allow a separate explicit upstream refresh."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--engine', default='podman', choices=['podman'])
    parser.add_argument('--refresh', action='store_true')
    args = parser.parse_args()
    # Refuse to snapshot another assistant's unfinished recipes during a refresh.
    if args.refresh and subprocess.check_output(
            ['git', 'status', '--porcelain'], cwd=ROOT, text=True):
        sys.exit('Commit changes before refreshing local bases')
    common = subprocess.check_output(['git', 'rev-parse', '--git-common-dir'],
                                     cwd=ROOT, text=True).strip()
    with (ROOT / common / 'alreadyon-local-bases.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        refs = ensure(args.engine, args.refresh)
    print(refs['ci'], refs['runtime'])


if __name__ == '__main__':
    main()
