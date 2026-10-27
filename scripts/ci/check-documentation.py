#!/usr/bin/env python3
"""Enforce documentation changes and source explanations on each submitted diff.

This structural gate cannot judge comment correctness. Reviewers must check the
public contracts, rationale and examples described in docs/CI_CD.md.
"""
import os
import pathlib
import re
import subprocess
import sys

SOURCE_SUFFIXES = {'.go', '.ts', '.tsx', '.js', '.jsx', '.cjs', '.mjs', '.py',
                   '.sh', '.sql', '.css', '.kt', '.swift'}
# Require words, excluding shebangs, directives and bare comment delimiters.
COMMENT = re.compile(r'(?m)^\s*(?://|/\*|\*|--|\#(?![!]))\s*[A-Za-z][^\n]{12,}|[\"\x27]{3}[A-Za-z][^\n]{12,}')


def needs_documentation(name):
    """Include executable code and infrastructure, but exclude generated locks."""
    path = pathlib.PurePosixPath(name)
    return (path.suffix in SOURCE_SUFFIXES or path.name in {'Dockerfile', 'Makefile'}
            or name.startswith(('.github/workflows/', '.github/actions/')))


def namespace_only(name, lines):
    """Recognize package/import path edits that do not alter executable logic."""
    suffix = pathlib.PurePosixPath(name).suffix
    if suffix == '.go':
        declaration = re.compile(r'(?:package\s+[A-Za-z_]\w*|(?:[A-Za-z_]\w*\s+)?"[^"\n]+")\Z')
    elif suffix == '.kt':
        declaration = re.compile(r'(?:package|import)\s+[\w.*]+(?:\s+as\s+\w+)?\Z')
    else:
        return False
    changed = [line.strip() for line in lines if line.strip()]
    return bool(changed) and all(declaration.fullmatch(line) for line in changed)


def dependency_pins_only(changes, read_diff):
    """Version-only automated maintenance does not change a documented contract."""
    if not changes or read_diff is None:
        return False
    manifests = {'go.mod', 'go.sum', 'package.json', 'package-lock.json',
                 'build.gradle.kts', 'settings.gradle.kts', 'gradle.properties',
                 'gradle-wrapper.properties', 'gradle-wrapper.jar', 'libs.versions.toml'}
    for status, name in changes:
        if status != 'M':
            return False
        if pathlib.PurePosixPath(name).name in manifests:
            continue
        if name.startswith(('.github/workflows/', '.github/actions/')):
            lines = [line.strip() for line in read_diff(name) if line.strip()]
            if lines and all(re.fullmatch(r'(?:- )?uses: [A-Za-z0-9_./-]+@[A-Za-z0-9_.-]+', line)
                             for line in lines):
                continue
        return False
    return True


def check_changes(changes, read_text, read_diff=None):
    """Return actionable failures; deletions still require a documentation update."""
    sources = [(status, name) for status, name in changes if needs_documentation(name)]
    errors = []
    if sources and not dependency_pins_only(changes, read_diff) and not any(status != 'D' and name.endswith('.md') for status, name in changes):
        errors.append('Code/infrastructure changed: update a relevant Markdown document in this diff.')
    for status, name in sources:
        if status != 'D' and not COMMENT.search(read_text(name)):
            if read_diff is not None and namespace_only(name, read_diff(name)):
                continue
            errors.append(f'{name}: add an explanatory source comment or module docstring (at least 13 characters).')
    return errors


def changed_lines(base, name):
    """Return added and removed source lines for one path relative to base."""
    diff = subprocess.check_output(
        ['git', 'diff', '--no-ext-diff', '--unified=0', base, '--', name], text=True)
    return [line[1:] for line in diff.splitlines()
            if line.startswith(('+', '-')) and not line.startswith(('+++', '---'))]


def changed_files(base):
    """Use NUL-separated paths, treating renames as removal plus addition."""
    fields = subprocess.check_output(
        ['git', 'diff', '--name-status', '--no-renames', '-z', base], text=True).split('\0')
    return list(zip(fields[0::2], fields[1::2]))


if __name__ == '__main__':
    base = os.environ.get('CI_BASE')
    if base is None:
        # A clean checkout still validates its latest committed change. Using
        # HEAD silently checked an empty diff after every local commit.
        parent = subprocess.run(['git', 'rev-parse', '--verify', 'HEAD^'],
                                capture_output=True, text=True)
        base = parent.stdout.strip() if parent.returncode == 0 else subprocess.check_output(
            ['git', 'hash-object', '-t', 'tree', '--stdin'], input='', text=True).strip()
    if not base or set(base) == {'0'}:
        # A new branch has no push.before; compare its fork point with dev.
        result = subprocess.run(['git', 'merge-base', 'HEAD', 'origin/dev'],
                                capture_output=True, text=True)
        base = result.stdout.strip() if result.returncode == 0 else subprocess.check_output(
            ['git', 'hash-object', '-t', 'tree', '--stdin'], input='', text=True).strip()
    errors = check_changes(changed_files(base), lambda name: pathlib.Path(name).read_text(),
                           lambda name: changed_lines(base, name))
    for error in errors:
        print(error, file=sys.stderr)
    if errors:
        sys.exit(1)
    print('Documentation and source-comment checks passed (semantic review still required).')
