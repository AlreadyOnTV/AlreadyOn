"""Run promotion shell paths with synthetic helpers; never publish a release."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import yaml

ROOT = Path(__file__).resolve().parents[2]
SHA = 'a' * 40


class PromotionOutputs(unittest.TestCase):
    def test_direct_stable_only_runs_through_validated_manual_dev_build(self):
        workflow = yaml.safe_load((ROOT / '.github/workflows/android-release.yml').read_text())
        guard = workflow['jobs']['candidate']['if']
        for condition in ("inputs.operation == 'direct-stable'", "github.event_name == 'workflow_dispatch'",
                          "github.ref == 'refs/heads/dev'", "github.workflow == 'Build Date Release Image'"):
            self.assertIn(condition, guard)
        parent = yaml.safe_load((ROOT / '.github/workflows/rebuild.yml').read_text())
        self.assertIn("inputs.publication == 'stable'", parent['jobs']['android']['with']['operation'])
        self.assertIn('tests', parent['jobs']['android']['needs'])
        self.assertIn('publish', parent['jobs']['android']['needs'])
        self.assertIn('codeql', parent['jobs']['publish']['needs'])

    def test_push_and_dispatch_outputs_are_machine_readable(self):
        workflow = yaml.safe_load((ROOT / '.github/workflows/android-release.yml').read_text())
        for job, step_name in [('promotion', 'Select tested source and release date')]:
            script = next(s['run'] for s in workflow['jobs'][job]['steps'] if s.get('name') == step_name)
            for event in ('push', 'workflow_dispatch'):
                for day in ('', '26.10.11'):
                    with self.subTest(job=job, event=event, day=day), tempfile.TemporaryDirectory() as tmp:
                        root = Path(tmp)
                        helpers = root / 'scripts/ci'
                        helpers.mkdir(parents=True)
                        (helpers / 'branch-policy.py').write_text(
                            "import os\nwith open(os.environ['GITHUB_OUTPUT'],'a') as f: f.write('selected=" + SHA + "\\n')\nprint('Branch and promotion policy passed')\n")
                        (helpers / 'release-context.py').write_text("print('26.10.11')\n")
                        (helpers / 'android-release.py').write_text(
                            "import sys\nassert sys.argv[1:] == ['promote-stable','--sha','" + SHA + "','--day','26.10.11']\nprint('Published synthetic promotion')\n")
                        output = root / 'output'
                        output.touch()
                        env = dict(os.environ, GITHUB_EVENT_NAME=event, SOURCE_SHA=SHA, RELEASE_DATE=day,
                                   GITHUB_OUTPUT=str(output), GITHUB_STEP_SUMMARY=str(root / 'summary'))
                        result = subprocess.run(['bash', '-euo', 'pipefail', '-c', script], cwd=root, env=env,
                                                text=True, capture_output=True, check=True)
                        lines = output.read_text().splitlines()
                        self.assertTrue(all('=' in line for line in lines), lines)
                        self.assertIn('selected=' + SHA, lines)
                        if event == 'push':
                            self.assertIn('Branch and promotion policy passed', result.stdout)
                        self.assertNotIn('Branch and promotion policy passed', output.read_text())


if __name__ == '__main__':
    unittest.main()
