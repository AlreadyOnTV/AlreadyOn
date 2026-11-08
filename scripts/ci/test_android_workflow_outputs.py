"""Verify the main-only Nightly and Stable workflow contract."""
from pathlib import Path
import unittest
import yaml

ROOT = Path(__file__).resolve().parents[2]


class ReleaseWorkflowContract(unittest.TestCase):
    def test_main_merge_publishes_nightly_image_and_apks(self):
        build = yaml.safe_load((ROOT / '.github/workflows/rebuild.yml').read_text())
        self.assertEqual(build[True]['push']['branches'], ['main'])
        self.assertEqual(build['jobs']['android']['with']['operation'], 'nightly')
        self.assertEqual(build['jobs']['android']['needs'], ['select', 'candidate'])
        candidate_steps = build['jobs']['candidate']['steps']
        self.assertIn('Record exact validated image for APK provenance',
                  [step.get('name') for step in candidate_steps])
        self.assertEqual(build['jobs']['channels']['needs'], ['select', 'candidate', 'android'])
        image_step = next(step for step in build['jobs']['channels']['steps']
                  if step.get('name') == 'Publish Nightly and today\'s date aliases')
        self.assertIn('CI_SOURCE_BRANCH=main', image_step['run'])
        self.assertIn('CI_PUBLICATION=nightly', image_step['run'])

    def test_stable_dispatch_reuses_latest_nightly_image_and_source(self):
        stable = yaml.safe_load((ROOT / '.github/workflows/release.yml').read_text())
        self.assertIn('workflow_dispatch', stable[True])
        self.assertEqual(stable['jobs']['select']['if'], "github.ref_name == 'main'")
        selector = stable['jobs']['select']['steps'][-1]['run']
        self.assertIn('plan-latest-stable', selector)
        image = stable['jobs']['image']['steps'][-1]['run']
        self.assertIn('CI_PUBLICATION=latest', image)
        android = stable['jobs']['android']
        self.assertEqual(android['with']['operation'], 'stable')
        self.assertEqual(android['with']['source_sha'], '${{ needs.select.outputs.source_sha }}')
        self.assertEqual(android['with']['predecessor'], '${{ needs.select.outputs.predecessor }}')
        self.assertEqual(android['needs'], 'select')
        self.assertEqual(stable['jobs']['image']['needs'], ['select', 'android'])
        self.assertIn('CI_SELECTED_SHA', image)

    def test_android_workflow_has_only_nightly_and_stable_operations(self):
        android = yaml.safe_load((ROOT / '.github/workflows/android-release.yml').read_text())
        self.assertEqual(android['jobs']['candidate']['if'],
                         "inputs.operation == 'nightly' && github.ref == 'refs/heads/main'")
        self.assertEqual(android['jobs']['promotion']['if'],
                         "inputs.operation == 'stable' && github.ref == 'refs/heads/main'")


if __name__ == '__main__':
    unittest.main()
