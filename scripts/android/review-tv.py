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
    key(20)
    down = focused(snapshot())
    assert down and down != initial, 'Down must move to another channel'
    key(19)
    assert focused(snapshot()) == initial, 'Up must return to the previous channel'
    current = selected_program(snapshot())
    key(22)
    tree = snapshot()
    assert focused(tree) == initial, 'Horizontal browsing must retain the channel row'
    assert selected_program(tree) and selected_program(tree) != current, 'Right must update the selected program and details'
    key(21)
    assert selected_program(snapshot()) == current, 'Left must return to the previous program and details'
    print('PASS: guide arrows', flush=True)
    key(23)
    tree = snapshot()
    assert 'CHANNEL' not in texts(tree), 'Select must enter fullscreen'
    key(19)
    key(23) # Pause keeps screenshots stable while checking focus; no settings are changed.
    assert focused(snapshot()) == 'Play', 'Pause must retain transport focus'
    key(20)
    key(19)
    assert focused(snapshot()) == 'Play', 'Overlay Up/Down must retain transport focus'
    key(22)
    assert focused(snapshot()) in ('Mute', 'Unmute'), 'Right must reach the mute control'
    key(22)
    assert focused(snapshot()) == 'Settings', 'Right must reach Settings'
    key(23)
    tree = snapshot()
    assert {'Quality', 'Audio Tracks', 'Subtitles'} <= texts(tree), 'Settings menu must open'
    print('PASS: fullscreen overlay navigation and Settings', flush=True)
    key(23) # Quality is the first root menu option.
    tree = snapshot()
    assert focused(tree).startswith('✓ '), 'Quality must focus its current setting'
    options = [n.get('text', '').removeprefix('✓ ') for n in tree.iter('node')
               if n.get('text', '').removeprefix('✓ ') in ('Auto', '1080p', '720p', '480p', '360p')]
    assert options == sorted(options, key=('Auto', '1080p', '720p', '480p', '360p').index), 'Quality must descend after Auto'
    key(4)
    key(20)
    key(23)
    tree = snapshot()
    if 'No audio tracks' not in texts(tree):
        assert focused(tree).startswith('✓ '), 'Audio must focus its active track'
    key(4)
    key(20)
    key(20)
    key(23)
    assert focused(snapshot()).startswith('✓ '), 'Subtitles must focus its current setting'
    key(4)
    key(4)
    tree = snapshot()
    assert 'CHANNEL' not in texts(tree), 'Menu Back must retain fullscreen'
    assert not {'Quality', 'Audio Tracks', 'Subtitles'} <= texts(tree), 'Back must close Settings'
    for _ in range(2):
        key(4)
        tree = snapshot()
        if 'CHANNEL' in texts(tree):
            break
    assert 'CHANNEL' in texts(tree), 'Back must return to guide'
    key(20)
    key(4)
    assert focused(snapshot()) == initial, 'Guide Back must restore the previewed channel'
    key(4)
    assert focused(snapshot()) == 'Play', 'Second guide Back must focus mini-player transport'
    key(23) # Resume in the mini-player; focus must remain on transport.
    assert focused(snapshot()) == 'Pause', 'Resume must retain transport focus'
    key(20)
    assert focused(snapshot()) == initial, 'Mini-player Down must restore guide focus'
    key(4)
    key(19)
    assert focused(snapshot()) == 'Dashboard', 'Mini-player Up must open and focus Dashboard'
    key(4)
    assert focused(snapshot()) == 'Pause', 'Profile-menu Back must restore mini-player focus'
    key(4)
    assert not any(n.get('package') == package for n in snapshot().iter('node')), 'Mini-player Back must exit the application'
    # Force-stop tests process death, preserving Keystore cookies and profile preferences.
    run('shell', 'am', 'force-stop', package)
    run('shell', 'am', 'start', '-n', f'{package}/io.github.alreadyonapp.alreadyon.MainActivity')
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        restored = texts(snapshot())
        if 'CHANNEL' in restored or 'Select Profile' in restored:
            print('PASS: guide arrows, overlay focus, current submenu settings, pause/resume, guide Back, mini-player/profile navigation, exit and session restoration')
            return
    raise AssertionError('Saved session did not restore the guide after process death')


if __name__ == '__main__':
    main()
