#!/usr/bin/env python3
"""Build the dependency-free product site and verify its local links."""

from html.parser import HTMLParser
import hashlib
from pathlib import Path
import shutil
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / 'website'
OUTPUT = ROOT / '_site'


class Links(HTMLParser):
    """Collect page references and fragment targets for static-site validation."""

    def __init__(self):
        super().__init__()
        self.links = []
        self.ids = set()

    def handle_starttag(self, tag, attrs):
        attributes = dict(attrs)
        if attributes.get('id'):
            self.ids.add(attributes['id'])
        for name in ('href', 'src', 'data-image'):
            if attributes.get(name):
                self.links.append(attributes[name])


def main():
    # Only remove our dedicated generated output, never application or media data.
    if OUTPUT.exists():
        shutil.rmtree(OUTPUT)
    shutil.copytree(SOURCE, OUTPUT, ignore=shutil.ignore_patterns('README.md'))
    branding = OUTPUT / 'assets' / 'branding'
    branding.mkdir(parents=True, exist_ok=True)
    for asset in ('header-doc.svg', 'icon.svg'):
        shutil.copy2(ROOT / 'web' / 'public' / 'branding' / asset, branding / asset)
    (OUTPUT / '.nojekyll').touch()
    # Content-addressed screenshot URLs prevent stale browser/CDN captures after updates.
    replacements = {}
    for screenshot in (OUTPUT / 'assets' / 'screenshots').glob('*.png'):
        digest = hashlib.sha256(screenshot.read_bytes()).hexdigest()[:16]
        versioned = screenshot.with_name(f'{screenshot.stem}.{digest}{screenshot.suffix}')
        replacements[screenshot.relative_to(OUTPUT).as_posix()] = versioned.relative_to(OUTPUT).as_posix()
        screenshot.rename(versioned)
    for page in OUTPUT.rglob('*.html'):
        content = page.read_text()
        for original, versioned in replacements.items():
            content = content.replace(original, versioned)
        page.write_text(content)
    pages = {}
    for page in OUTPUT.rglob('*.html'):
        parser = Links()
        parser.feed(page.read_text())
        pages[page] = parser
    failures = []
    for page, parser in pages.items():
        for link in parser.links:
            url = urlsplit(link)
            if url.scheme or url.netloc:
                continue
            target = (page.parent / unquote(url.path)).resolve() if url.path else page
            if target.is_dir():
                target = target / 'index.html'
            if not target.is_relative_to(OUTPUT):
                failures.append(f'{page.name}: reference escapes site: {link}')
            elif not target.is_file():
                failures.append(f'{page.name}: missing asset/page: {link}')
            elif url.fragment and target in pages and unquote(url.fragment) not in pages[target].ids:
                failures.append(f'{page.name}: missing anchor: {link}')
    if failures:
        raise SystemExit('\n'.join(failures))
    print(f'Built {len(pages)} pages; all local links, anchors and assets validated.')


if __name__ == '__main__':
    main()
