#!/usr/bin/env python3
"""Preserve old GitHub project-site bookmarks after moving to the organization site."""
from pathlib import Path
import shutil

output = Path(__file__).resolve().parents[1] / '_site'
if output.exists():
    shutil.rmtree(output)
output.mkdir()
for page, destination in [('index.html', ''), ('guides.html', 'guides.html'), ('help.html', 'help.html')]:
    url = 'https://alreadyontv.com/' + destination
    # JavaScript preserves query strings and fragments; the visible link is the fallback.
    (output / page).write_text(f'''<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>AlreadyOn has moved</title><link rel="canonical" href="{url}"><script>location.replace({url!r} + location.search + location.hash);</script></head><body><p>AlreadyOn has moved to <a href="{url}">{url}</a>.</p></body></html>''')
(output / '.nojekyll').touch()
print('Built legacy redirects to the organization root site.')
