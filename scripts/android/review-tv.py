#!/usr/bin/env python3
"""Review an explicitly selected, signed-in TV without clearing its data or changing server settings."""
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

    def selected_program(tree):
        return next((n.get('content-desc') for n in tree.iter('node')
                     if n.get('content-desc', '').startswith('Selected program: ')), '')

    tree = snapshot()
    if 'CHANNEL' not in texts(tree) or not all(n.get('package', package) == package for n in tree.iter('node')):
        parser.error('Start the selected TV review app in its signed-in guide before running. No login data is modified.')
    initial = focused(tree)
    if not initial.startswith('Channel '):
        parser.error('Focus a channel row in the guide before running the remote review.')
    key(4)
    assert 'Switch Profile' in texts(snapshot()), 'One Back from current airing opens profile menu'
    key(4)
    assert focused(snapshot()) == initial, 'Profile Back restores current guide row'
    key(20)
    assert focused(snapshot()) != initial, 'Down browses another channel'
    key(4)
    assert focused(snapshot()) == initial, 'Back from another row restores current airing'
    current = selected_program(snapshot())
    key(22)
    assert selected_program(snapshot()) != current, 'Right browses another airing'
    key(4)
    assert selected_program(snapshot()) == current, 'Back restores current airing'
    key(23)
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        if focused(snapshot()) in ('Play', 'Pause'):
            break
    else:
        raise AssertionError('Fullscreen must initialize Play/Pause focus')
    key(22)
    assert focused(snapshot()) in ('Mute', 'Unmute'), 'Right reaches mute'
    key(4)
    assert 'CHANNEL' not in texts(snapshot()), 'One Back hides overlay and retains fullscreen'
    key(22)
    assert focused(snapshot()) in ('Play', 'Pause'), 'Opening Right initializes Play/Pause'
    key(22)
    key(22)
    assert focused(snapshot()) == 'Settings', 'Horizontal cursor reaches Settings'
    key(23)
    tree = snapshot()
    labels = texts(tree)
    assert any(t.startswith('Quality (') for t in labels), 'Quality status is present'
    assert any(t == 'Audio Tracks' or t.startswith('Audio Tracks (') for t in labels), 'Audio menu is present with optional detected language'
    assert any(t in ('Subtitles (on)', 'Subtitles (off)') for t in labels), 'Subtitle status is present'
    key(23)
    assert '‹ Quality' in texts(snapshot()), 'Quality submenu opens'
    key(4)
    assert any(t.startswith('Quality (') for t in texts(snapshot())), 'Submenu Back returns to settings'
    key(4)
    assert 'CHANNEL' not in texts(snapshot()), 'Settings Back retains fullscreen'
    key(4)
    assert 'CHANNEL' not in texts(snapshot()), 'Overlay Back retains fullscreen'
    key(4)
    assert 'CHANNEL' in texts(snapshot()), 'Hidden-overlay Back returns to guide'
    key(4)
    assert 'Switch Profile' in texts(snapshot()), 'One Back after fullscreen return opens profile menu'
    key(4)
    assert focused(snapshot()).startswith('Channel '), 'Profile dismissal restores guide focus before tuning'
    key(23)
    assert 'CHANNEL' not in texts(snapshot()), 'Select enters fullscreen again'
    key(20)
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        tree = snapshot()
        if focused(tree) in ('Play', 'Pause'):
            break
    else:
        raise AssertionError('Channel switch must reset transport focus')
    key(4) # Hide before checking the opening Left press.
    key(21)
    assert focused(snapshot()) in ('Play', 'Pause'), 'Opening Left resets transport focus'
    key(4)
    key(4)
    print('PASS: physical TV guide Back, overlay reveal focus, single Back dismissal, nested menus, active status and channel switching', flush=True)


if __name__ == '__main__':
    main()
