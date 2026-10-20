#!/usr/bin/env python3
"""Keep contributor Markdown brief and local document links usable."""
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
paths = subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '*.md'], cwd=ROOT, text=True).splitlines()
errors = []
for name in sorted(set(paths)):
    path = ROOT / name
    if not path.exists():
        continue  # A tracked removal is absent before the commit.
    text = path.read_text()
    words = len(text.split())
    # Preserve the standard governance policy; it is not a short contributor guide.
    # Its links still receive the same validation as other Markdown files.
    if words > 500 and name != 'CODE_OF_CONDUCT.md':
        errors.append(f'{name}: {words} words; keep each guide within 500 words.')
    for target in re.findall(r'\]\(([^)]+)\)', text):
        target = target.split('#', 1)[0]
        if not target or '://' in target or target.startswith('mailto:'):
            continue
        if not (path.parent / target).exists():
            errors.append(f'{name}: missing local link {target}')
for error in errors:
    print(error, file=sys.stderr)
if errors:
    sys.exit(1)
print('Public Markdown length and local-link checks passed.')
