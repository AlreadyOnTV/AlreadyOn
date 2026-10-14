"""Test The installation's single-instance nightly sync against a fake Docker."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).parents[2] / 'deploy' / 'alreadyon-nightly-sync.sh'
IMAGE = 'ghcr.io/alreadyontv/alreadyon:nightly'

# Minimal Docker double: state.json records calls without creating containers or images.
FAKE_DOCKER = r'''#!/usr/bin/env python3
import json, os, sys
path = os.environ['FAKE_STATE']
state = json.load(open(path))
args = sys.argv[1:]
state['calls'].append(args)
out = ''
if args[0] == 'compose' and 'config' in args:
    out = state.get('compose_image', os.environ['NIGHTLY_IMAGE']) + '\n' if '--images' in args else ''
elif args[0] == 'compose' and 'up' in args:
    state['running'] = 'new'
elif args[0] == 'inspect':
    fmt = args[2] if len(args) > 2 else ''
    if '.Health' in fmt:
        out = state['running'] + (' unhealthy' if state.get('unhealthy') else ' healthy') + '\n'
    elif fmt:
        out = state['running'] + '\n'
elif args[:2] == ['image', 'inspect']:
    out = 'new\n' if '.Id' in args[3] else 'abcdef1234567890\n'
elif args[0] == 'pull' and state.get('pull_fails'):
    json.dump(state, open(path, 'w')); sys.exit(1)
json.dump(state, open(path, 'w'))
sys.stdout.write(out)
'''


class NightlySync(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        (self.root / 'bin').mkdir()
        for name, body in [('docker', FAKE_DOCKER), ('curl', '#!/bin/sh\nexit 0\n')]:
            (self.root / 'bin' / name).write_text(body)
            (self.root / 'bin' / name).chmod(0o755)
        self.app = self.root / 'app'
        self.app.mkdir()
        (self.app / 'docker-compose.yaml').write_text('services: {}')
        (self.app / 'alreadyon.db').write_text('live database placeholder')
        self.state = self.root / 'state.json'

    def run_sync(self, running='old', unhealthy=False, pull_fails=False,
                 compose_image=IMAGE, args=()):
        self.state.write_text(json.dumps({'calls': [], 'running': running,
                                          'unhealthy': unhealthy, 'pull_fails': pull_fails,
                                          'compose_image': compose_image}))
        env = dict(os.environ, PATH=f'{self.root}/bin:{os.environ["PATH"]}',
                   FAKE_STATE=str(self.state), APP_DIR=str(self.app),
                   NIGHTLY_IMAGE=IMAGE, HEALTH_TIMEOUT='1')
        result = subprocess.run(['bash', str(SCRIPT), *args], env=env,
                                capture_output=True, text=True)
        return result, json.loads(self.state.read_text())

    def test_new_image_recreates_the_one_service_without_artifacts(self):
        result, state = self.run_sync()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(state['running'], 'new')
        self.assertTrue(any(call[0] == 'compose' and 'up' in call for call in state['calls']))
        self.assertFalse(any(call[0] in {'tag', 'stop', 'run'} for call in state['calls']))
        self.assertEqual(sorted(path.name for path in self.app.iterdir()),
                         ['alreadyon.db', 'docker-compose.yaml'])

    def test_current_image_is_left_alone(self):
        result, state = self.run_sync(running='new')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any(call[0] == 'compose' and 'up' in call for call in state['calls']))
        self.assertIn('nothing to do', result.stdout)

    def test_unhealthy_candidate_is_reported_without_making_a_copy(self):
        result, state = self.run_sync(unhealthy=True)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(state['running'], 'new')
        self.assertFalse(any(call[0] in {'tag', 'stop', 'run'} for call in state['calls']))
        self.assertIn('did not become healthy', result.stderr)

    def test_pull_failure_changes_nothing(self):
        result, state = self.run_sync(pull_fails=True)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(state['running'], 'old')
        self.assertFalse(any(call[0] == 'compose' and 'up' in call for call in state['calls']))

    def test_noncanonical_compose_image_is_rejected(self):
        result, state = self.run_sync(compose_image='alreadyon:latest')
        self.assertEqual(result.returncode, 1)
        self.assertFalse(any(call[0] == 'pull' for call in state['calls']))
        self.assertIn('must run ' + IMAGE, result.stderr)


if __name__ == '__main__':
    unittest.main()
