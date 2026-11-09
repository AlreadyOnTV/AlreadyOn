"""Verify the manual dev/main Nightly and main-only Stable workflow contract."""
from pathlib import Path
import unittest
import yaml

ROOT = Path(__file__).resolve().parents[2]


class ReleaseWorkflowContract(unittest.TestCase):
    def test_manual_nightly_dispatch_publishes_from_dev_or_main(self):
        build = yaml.safe_load((ROOT / '.github/workflows/build_publish.yml').read_text())
        self.assertIn('workflow_dispatch', build[True])
        self.assertEqual(build[True]['push']['branches'], ['main'])
        self.assertIn('dev|main', build['jobs']['select']['steps'][-1]['run'])
        self.assertEqual(build['jobs']['android']['with']['operation'], 'nightly')
        self.assertEqual(build['jobs']['android']['needs'],
                 ['select', 'candidate', 'security_scan', 'record_validated_image'])
        candidate_steps = build['jobs']['candidate']['steps']
        self.assertIn('Record the scanned image as validated for this exact source',
                  [step.get('name') for step in build['jobs']['record_validated_image']['steps']])
        self.assertEqual(build['jobs']['channels']['needs'],
                 ['select', 'candidate', 'security_scan', 'record_validated_image', 'android'])
        image_step = next(step for step in build['jobs']['channels']['steps']
                  if step.get('name') == 'Publish Nightly and today\'s date aliases')
        self.assertIn('CI_SOURCE_BRANCH="$GITHUB_REF_NAME"', image_step['run'])
        self.assertIn('CI_PUBLICATION=nightly', image_step['run'])

    def test_stable_dispatch_reuses_latest_nightly_image_and_source(self):
        stable = yaml.safe_load((ROOT / '.github/workflows/build_publish.yml').read_text())
        self.assertIn('workflow_dispatch', stable[True])
        self.assertIn('stable', stable[True]['workflow_dispatch']['inputs']['operation']['options'])
        self.assertEqual(stable['jobs']['stable_select']['if'],
                 "github.event_name == 'workflow_dispatch' && inputs.operation == 'stable'")
        selector = stable['jobs']['stable_select']['steps'][-1]['run']
        self.assertIn('plan-latest-stable', selector)
        image = stable['jobs']['stable_image']['steps'][-1]['run']
        self.assertIn('CI_PUBLICATION=latest', image)
        android = stable['jobs']['android']
        self.assertEqual(android['with']['operation'], 'nightly')
        android = stable['jobs']['stable_android']
        self.assertEqual(android['with']['operation'], 'stable')
        self.assertEqual(android['with']['source_sha'], '${{ needs.stable_select.outputs.source_sha }}')
        self.assertEqual(android['with']['predecessor'], '${{ needs.stable_select.outputs.predecessor }}')
        self.assertEqual(android['needs'], 'stable_select')
        self.assertEqual(stable['jobs']['stable_image']['needs'], ['stable_select', 'stable_android'])
        self.assertIn('CI_SELECTED_SHA', image)

    def test_android_workflow_has_only_nightly_and_stable_operations(self):
        android = yaml.safe_load((ROOT / '.github/workflows/android_release.yml').read_text())
        self.assertEqual(android['jobs']['candidate']['if'],
                         "inputs.operation == 'nightly' && (github.ref == 'refs/heads/dev' || github.ref == 'refs/heads/main')")
        self.assertEqual(android['jobs']['promotion']['if'],
                         "inputs.operation == 'stable' && github.ref == 'refs/heads/main'")


if __name__ == '__main__':
    unittest.main()
