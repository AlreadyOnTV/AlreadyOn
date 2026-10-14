"""Exercise failed signing, staged uploads, tampered inventories and missed schedules offline."""
from datetime import datetime, timedelta, timezone
import importlib.util
from pathlib import Path
import subprocess
import os
import runpy
import sys
import tempfile
import json
import unittest
from unittest.mock import patch, Mock


def load(name, directory=None):
    spec = importlib.util.spec_from_file_location(name.replace('-', '_'), (directory or Path(__file__).parent) / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class SupplyChain(unittest.TestCase):
    def test_staging_never_changes_channels_or_records_a_release(self):
        stage = load('stage-image')
        digest = 'sha256:' + 'd' * 64
        with patch.object(stage.subprocess, 'run') as run, patch.object(stage.subprocess, 'check_output', return_value='Digest: ' + digest):
            result = stage.stage('Owner/Repo', 'tested-image', '123', '2')
        self.assertEqual(result['source'], 'ghcr.io/owner/repo@' + digest)
        self.assertEqual([call.args[0] for call in run.call_args_list], [
            ['docker', 'tag', 'tested-image', 'ghcr.io/owner/repo:rebuild-123-2'],
            ['docker', 'push', 'ghcr.io/owner/repo:rebuild-123-2']])

    def test_failed_upload_produces_no_digest_for_attestation(self):
        stage = load('stage-image')
        with patch.object(stage.subprocess, 'run', side_effect=subprocess.CalledProcessError(1, 'docker')), \
                patch.object(stage.subprocess, 'check_output') as inspect:
            with self.assertRaises(subprocess.CalledProcessError):
                stage.stage('owner/repo', 'tested-image', '123', '1')
            inspect.assert_not_called()

    def test_new_cycles_require_both_signatures_and_fail_on_missing_inventory(self):
        verify = load('verify-image')
        source = 'ghcr.io/owner/repo@sha256:' + 'd' * 64
        run = Mock(side_effect=[None, subprocess.CalledProcessError(1, 'gh')])
        with patch.object(verify.subprocess, 'check_output', return_value='26-10-18\n'):
            with self.assertRaises(subprocess.CalledProcessError):
                verify.verify('owner/repo', source, 'a' * 40, run)
        self.assertEqual(run.call_count, 2)
        self.assertIn('--signer-workflow', run.call_args_list[0].args[0])
        self.assertIn('https://cyclonedx.org/bom', run.call_args_list[1].args[0])
        with patch.object(verify.subprocess, 'check_output', return_value='26-10-11\n'):
            run.reset_mock()
            verify.verify('owner/repo', 'legacy-sha-tag', 'a' * 40, run)
            run.assert_not_called()

    def test_mutable_source_cannot_enter_new_attested_promotion(self):
        verify = load('verify-image')
        with patch.object(verify.subprocess, 'check_output', return_value='26-10-18\n'):
            with self.assertRaises(ValueError): verify.verify('owner/repo', 'ghcr.io/owner/repo:nightly', 'a' * 40)

    def test_manual_or_failed_review_does_not_hide_a_missed_schedule(self):
        health = load('maintenance-health').health
        now = datetime(2026, 10, 13, tzinfo=timezone.utc)
        def run(age, event='schedule', conclusion='success'):
            return {'event': event, 'conclusion': conclusion, 'updated_at': (now - timedelta(days=age)).isoformat()}
        self.assertTrue(health([run(7)], now))
        for runs in [[], [run(9), run(0, 'workflow_dispatch')], [run(0, conclusion='failure')], [run(-1)]]:
            with self.assertRaises(ValueError): health(runs, now)

    def test_channel_digest_mismatch_never_records_success(self):
        import image_registry
        path = Path(__file__).with_name('publish-image.py')
        first = 'sha256:' + 'd' * 64
        other = 'sha256:' + 'e' * 64
        with patch.dict(os.environ, GITHUB_REPOSITORY='owner/repo', CI_REVISION='a' * 40,
                        SOURCE_IMAGE='ghcr.io/owner/repo@' + first,
                        IMAGE_TAGS='ghcr.io/owner/repo:nightly ghcr.io/owner/repo:26-10-18'), \
                patch.object(image_registry, 'record') as record, \
                patch('subprocess.run'), \
                patch('subprocess.check_output', side_effect=['Digest: ' + first, 'Digest: ' + other]), \
                patch.object(sys, 'argv', [str(path)]):
            with self.assertRaisesRegex(ValueError, 'digests do not match'):
                runpy.run_path(str(path), run_name='__main__')
            record.assert_not_called()

    def test_public_image_inventory_discards_private_scanner_fields(self):
        root = Path(__file__).resolve().parents[2]
        with tempfile.TemporaryDirectory() as directory:
            report, output = Path(directory) / 'report.json', Path(directory) / 'inventory.json'
            report.write_text(json.dumps({'ArtifactName': '/private/server', 'Results': [{
                'Secrets': [{'Match': 'synthetic-private-value'}],
                'Vulnerabilities': [{'VulnerabilityID': 'CVE-synthetic'}],
                'Packages': [{'Name': 'safe-package', 'Version': '1.0',
                    'Identifier': {'PURL': 'pkg:deb/ubuntu/safe-package@1.0'}, 'FilePath': '/private/config'}]}]}))
            source = 'ghcr.io/owner/repo@sha256:' + 'd' * 64
            subprocess.run([sys.executable, str(root / 'scripts/security/image-inventory.py'),
                '--report', str(report), '--image', source, '--revision', 'a' * 40, '--output', str(output)], check=True)
            bom = json.loads(output.read_text())
            self.assertEqual(bom['metadata']['component']['name'], source)
            self.assertEqual(bom['metadata']['component']['hashes'][0]['content'], 'd' * 64)
            for private in ['synthetic-private-value', 'CVE-synthetic', '/private/', 'Vulnerabilities']:
                self.assertNotIn(private, output.read_text())
            self.assertEqual(bom['components'][0]['name'], 'safe-package')

    def test_channel_mutation_is_after_attestation_verification(self):
        root = Path(__file__).resolve().parents[2]
        workflow = (root / '.github/workflows/rebuild.yml').read_text()
        self.assertLess(workflow.index('stage-image.py'), workflow.index('actions/attest@v4'))
        self.assertLess(workflow.index('Verify both signed statements'), workflow.index('publish-image.py'))
        self.assertNotIn('push-to-registry: true', workflow) # GitHub retains signatures independently of OCI cleanup.
        promotion = (root / '.github/workflows/release.yml').read_text()
        self.assertLess(promotion.index('verify-image.py'), promotion.index('publish-image.py'))
        maintenance = (root / '.github/workflows/dependency-maintenance.yml').read_text()
        self.assertNotIn('packages: write', maintenance)
        self.assertNotIn('publish-image.py', maintenance)
