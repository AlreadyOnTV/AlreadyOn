"""Verify the manual dev/main Nightly and main-only Stable workflow contract."""
from pathlib import Path
import os
import subprocess
import tempfile
import unittest
import yaml

ROOT = Path(__file__).resolve().parents[2]


class ReleaseWorkflowContract(unittest.TestCase):
    def test_gradle_wrapper_allows_slow_distribution_downloads(self):
        wrapper = (ROOT / 'apps/android/gradle/wrapper/gradle-wrapper.properties').read_text()
        self.assertIn('networkTimeout=120000', wrapper)

    def test_pr_parallel_jobs_remain_mandatory_and_read_only(self):
        workflow = yaml.safe_load((ROOT / '.github/workflows/pull_request_validation.yml').read_text())
        jobs = workflow['jobs']
        gates = {'build_candidate', 'quality', 'android', 'android_candidate',
                 'candidate_scan', 'tests', 'dependency_review'}
        self.assertEqual(set(jobs['auto_merge']['needs']), gates)
        for gate in gates:
            self.assertIn(f"needs.{gate}.result == 'success'", jobs['auto_merge']['if'])
        self.assertEqual(jobs['tests']['needs'], ['build_candidate'])
        self.assertEqual(set(jobs['candidate_scan']['needs']), {'build_candidate', 'android_candidate'})
        self.assertEqual(workflow['permissions'], {'contents': 'read'})
        for name in ('android', 'android_candidate'):
            setup = next(step for step in jobs[name]['steps'] if step.get('uses', '').startswith('gradle/actions/setup-gradle@'))
            self.assertEqual(setup['with']['cache-read-only'], 'true')
        scan = next(step['run'] for step in jobs['candidate_scan']['steps'] if step.get('name') == 'Scan only shipped runtime components')
        self.assertIn('--packaged-only', scan)
        self.assertIn('/candidate/runtime.tar', scan)
        self.assertIn('/candidate/android-runtime', scan)

    def test_retargeted_dependency_prs_validate_and_merge_only_into_dev(self):
        workflow = yaml.safe_load((ROOT / '.github/workflows/pull_request_validation.yml').read_text())
        codeql = yaml.safe_load((ROOT / '.github/workflows/codeql.yml').read_text())
        self.assertIn("github.event.action == 'edited' && github.event.changes.base.ref == null && 'metadata' || 'candidate'",
                      workflow['concurrency']['group'])
        self.assertIn('edited', workflow[True]['pull_request']['types'])
        self.assertIn('edited', codeql[True]['pull_request']['types'])
        condition = "github.event.action != 'edited' || github.event.changes.base.ref != null"
        for job in ('build_candidate', 'quality', 'android', 'android_candidate',
                    'candidate_scan', 'tests', 'dependency_review'):
            self.assertEqual(workflow['jobs'][job]['if'], condition)
        self.assertEqual(codeql['jobs']['analyze']['if'], condition)
        self.assertIn("github.event.pull_request.user.login != 'dependabot[bot]' || github.event.pull_request.base.ref == 'dev'",
                      workflow['jobs']['auto_merge']['if'])
        dependabot = yaml.safe_load((ROOT / '.github/dependabot.yml').read_text())
        for update in dependabot['updates']:
            self.assertEqual(update['target-branch'], 'dev')
            self.assertGreater(update.get('open-pull-requests-limit', 5), 0)

    def test_cache_warmer_only_runs_trusted_main_without_publication(self):
        warmer = yaml.safe_load((ROOT / '.github/workflows/validation_cache.yml').read_text())
        self.assertEqual(set(warmer[True]), {'push', 'schedule'})
        self.assertEqual(warmer[True]['push']['branches'], ['main'])
        self.assertEqual(warmer['permissions'], {'contents': 'read'})
        for job in warmer['jobs'].values():
            checkout = next(step for step in job['steps'] if step.get('uses', '').startswith('actions/checkout@'))
            self.assertEqual(checkout['with']['ref'], 'main')
            self.assertFalse(checkout['with']['persist-credentials'])
        build = next(step for step in warmer['jobs']['runtime']['steps']
                     if step.get('uses', '').startswith('docker/build-push-action@'))
        self.assertTrue(build['with']['pull'])
        self.assertEqual(build['with']['no-cache'], "${{ github.event_name == 'schedule' }}")
        self.assertNotIn('secrets.', str(warmer))

    def test_source_action_mounts_go_caches_only_when_requested(self):
        action = yaml.safe_load((ROOT / '.github/actions/test-source/action.yml').read_text())
        step = next(step for step in action['runs']['steps'] if step.get('name') == 'Go tests and Chromium playback tests')
        script = step['run'].replace('${{ inputs.ci-base }}', 'ci:test')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = root / 'bin'
            binary.mkdir()
            commands = {
                'git': "#!/bin/sh\nprintf '%s\\n' aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n",
                'go': "#!/bin/sh\ncase \"$2\" in GOMODCACHE) printf '%s\\n' \"$FAKE_CACHE/mod\";; GOCACHE) printf '%s\\n' \"$FAKE_CACHE/build\";; *) exit 1;; esac\n",
                'docker': "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_ARGS\"\n",
            }
            for name, content in commands.items():
                path = binary / name
                path.write_text(content)
                path.chmod(0o755)
            environment = dict(os.environ, PATH=str(binary) + os.pathsep + os.environ['PATH'],
                               RUNNER_TEMP=directory, GITHUB_WORKSPACE=directory,
                               GITHUB_RUN_ID='1', GITHUB_RUN_ATTEMPT='1',
                               TEST_MEMORY='5g', TEST_TMPFS='2g',
                               FAKE_CACHE=str(root / 'cache with spaces'), DOCKER_ARGS=str(root / 'args'))
            for enabled in ('true', 'false'):
                with self.subTest(enabled=enabled):
                    subprocess.run(['/bin/bash', '-euc', script], env=dict(environment, HOST_GO_CACHE=enabled), check=True)
                    arguments = (root / 'args').read_text().splitlines()
                    self.assertEqual('GOMODCACHE=/ci-cache/mod' in arguments, enabled == 'true')
                    self.assertEqual('GOCACHE=/ci-cache/build' in arguments, enabled == 'true')
                    self.assertEqual(str(root / 'cache with spaces/mod') + ':/ci-cache/mod' in arguments, enabled == 'true')
                    self.assertEqual(str(root / 'cache with spaces/build') + ':/ci-cache/build' in arguments, enabled == 'true')
                    self.assertIn('go clean -testcache', arguments[-1])
                    self.assertIn('make test-e2e', arguments[-1])

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
