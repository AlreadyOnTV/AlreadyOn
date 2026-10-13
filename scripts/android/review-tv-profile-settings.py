#!/usr/bin/env python3
"""Check TV profile settings using remote keys and discard drafts without changing live preferences."""
import argparse
from pathlib import Path
import subprocess
import time
import xml.etree.ElementTree as ET


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--serial', required=True)
    parser.add_argument('--sdk', type=Path, required=True)
    args = parser.parse_args()
    adb = [str(args.sdk / 'platform-tools/adb'), '-s', args.serial]
    package = 'io.github.alreadyonapp.alreadyon.tv.debug'

    def run(*words):
        return subprocess.check_output(adb + list(words), text=True, timeout=30)

    def key(code):
        run('shell', 'input', 'keyevent', str(code))
        time.sleep(0.1) # Match deliberate remote presses without awaiting UI-tree extraction.

    def snapshot():
        path = '/data/local/tmp/alreadyon-review.xml'
        for attempt in range(3):
            run('shell', 'rm', '-f', path)
            output = run('shell', 'uiautomator', 'dump', path)
            if 'dumped to' in output:
                return ET.fromstring(run('exec-out', 'cat', path))
            time.sleep(0.2)
        raise AssertionError('TV did not provide a fresh UI tree: ' + output.strip())

    def texts(tree):
        return {n.get('text') for n in tree.iter('node') if n.get('text')}

    def focused(tree):
        for node in tree.iter('node'):
            if node.get('focused') == 'true':
                return next((n.get('content-desc') for n in node.iter('node') if n.get('content-desc')), '') or ', '.join(n.get('text', '') for n in node.iter('node')).strip(', ')
        return ''

    run('shell', 'am', 'force-stop', package)
    run('shell', 'am', 'start', '-W', '-n', f'{package}/io.github.alreadyonapp.alreadyon.MainActivity')
    time.sleep(3)
    key(20)
    key(4)
    watching = focused(snapshot())
    key(4)
    assert focused(snapshot()) in ('Pause', 'Play')
    key(19)
    assert focused(snapshot()) == 'Dashboard'
    key(20)
    assert focused(snapshot()) == 'Profile Settings'
    key(23)
    tree = snapshot()
    assert 'CHANNEL' not in texts(tree) and 'Profile Settings' in texts(tree)
    assert focused(tree) == 'Display Name'
    for category, expected, target in [
        ('Icon', 'Remove Icon', '🦊'),
        ('PIN', 'Status', None),
        ('Time Format', 'Time Format', '▾'),
        ('Audio Language', 'Audio Language', '▾'),
        ('Subtitles', 'Default Language', 'by Default')]:
        key(20)
        tree = snapshot()
        assert any(expected in value for value in texts(tree)), (category, texts(tree))
        assert focused(tree) == category, 'Sidebar focus must load the card without Select'
        if category == 'Icon': assert 'Upload Custom Icon' not in texts(tree)
        if category == 'Time Format': assert 'Guide Display' not in texts(tree)
        if category == 'Audio Language': assert 'Preferred Audio Language' not in texts(tree)
        key(22)
        tree = snapshot()
        actual = focused(tree)
        assert (target in actual if target else actual in ('Set PIN', 'Clear PIN')), (category, actual)
        key(4)
        assert focused(snapshot()) == category, 'Back must return to its sidebar category'
        key(23)
        actual = focused(snapshot())
        assert (target in actual if target else actual in ('Set PIN', 'Clear PIN')), (category, actual)
        key(4)
        assert focused(snapshot()) == category, 'Back after Select must restore the sidebar'
        print('PASS: automatic card and Right/Select entry', category, flush=True)
    key(22)
    before = focused(snapshot())
    key(23)
    after = focused(snapshot())
    assert before != after and 'by Default' in after, 'Subtitle default button must toggle the draft'
    key(4)
    assert focused(snapshot()) == 'Subtitles', 'First Back must restore the current sidebar category'
    key(4)
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        tree = snapshot()
        if focused(tree) == watching: break
    else: raise AssertionError('Back must focus the watched channel after restoration')
    print('PASS: subtitle draft toggle and Back to watched channel; no live settings changed')



if __name__ == '__main__':
    main()
