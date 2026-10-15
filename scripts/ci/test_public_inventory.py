"""Public inventories exclude raw findings and remain bound to promoted bytes."""
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parents[1] / 'security'))
import public_inventory as inventory
SPEC = importlib.util.spec_from_file_location('sbom_android_release', Path(__file__).with_name('android-release.py'))
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)
SHA = 'a' * 40
IMAGE = 'ghcr.io/alreadyontv/alreadyon@sha256:' + 'b' * 64


class PublicInventory(unittest.TestCase):
    def test_raw_report_fields_do_not_enter_inventory(self):
        report = {'Results': [{'Target': '/private/path', 'Secrets': [{'Match': 'private-secret'}],
                              'Vulnerabilities': [{'Description': 'private exploit'}],
                              'Packages': [{'Name': 'package', 'Version': '1',
                                            'Identifier': {'PURL': 'pkg:npm/package@1'},
                                            'Licenses': ['MIT'], 'FilePath': '/private/path'}]}]}
        encoded = json.dumps(inventory.inventory(IMAGE, 'b' * 64, SHA, inventory.image_components(report), 'Detected packages.'))
        for value in ('private', 'Secrets', 'Vulnerabilities', 'FilePath'):
            self.assertNotIn(value, encoded)
        self.assertIn('MIT', encoded)

    def bundle(self, directory):
        (directory / release.ASSETS[0]).write_bytes(b'signed phone fixture')
        (directory / release.ASSETS[3]).write_bytes(b'signed TV fixture')
        manifest = {'schema': 1, 'source_revision': SHA, 'files': {}, 'subjects': {}}
        for name, subject, checksum in [(inventory.NAMES[0], IMAGE, 'b' * 64),
                                        (inventory.NAMES[1], release.ASSETS[0], inventory.digest(directory / release.ASSETS[0])),
                                        (inventory.NAMES[2], release.ASSETS[3], inventory.digest(directory / release.ASSETS[3]))]:
            bom = inventory.inventory(subject, checksum, SHA, [inventory.package_component('fixture', '1', 'pkg:npm/fixture@1')], 'Synthetic inventory.')
            (directory / name).write_text(json.dumps(bom))
            manifest['files'][name] = inventory.digest(directory / name)
            manifest['subjects'][name] = {'name': subject, 'sha256': checksum}
        (directory / 'sbom-manifest.json').write_text(json.dumps(manifest))
        with patch.object(release, 'publish'), patch.object(release, 'releases', return_value=[]), \
                patch.object(release, 'reviewed_changes', return_value='# New Features\n\n- Synthetic feature.'):
            release.candidate('owner/repo', '26.10.11-RC2', SHA, 200000001, directory)

    def test_verified_inventory_matches_exact_apk_image_and_source(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            self.bundle(directory)
            self.assertIn('inventory', release.verify_assets(directory, SHA))
            inventory.verify_bundle(directory, SHA, IMAGE, directory / release.ASSETS[0], directory / release.ASSETS[3])
            for revision, image in [('c' * 40, IMAGE), (SHA, IMAGE[:-1] + 'c')]:
                with self.assertRaises(ValueError):
                    inventory.verify_bundle(directory, revision, image)

    def test_changed_inventory_or_subject_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            self.bundle(directory)
            (directory / inventory.NAMES[1]).write_text('{}')
            with self.assertRaisesRegex(ValueError, 'SBOM checksum'):
                release.verify_assets(directory, SHA)

    def test_promotions_download_all_inventory_bytes_and_reject_partial_bundle(self):
        assets = [{'name': name} for name in (*release.ASSETS, *release.SBOM_ASSETS)]
        with patch.object(release, 'releases', return_value=[{'tag_name': '26.10.11-RC2', 'assets': assets}]), \
                patch.object(release, 'run') as command:
            release.download('owner/repo', '26.10.11-RC2', Path('assets'))
            self.assertEqual(command.call_count, 2)
            for name in release.SBOM_ASSETS:
                self.assertIn(name, command.call_args.args)
        with patch.object(release, 'releases', return_value=[{'tag_name': '26.10.11-RC2', 'assets': assets[:-1]}]), \
                patch.object(release, 'run'), self.assertRaisesRegex(ValueError, 'Incomplete remote'):
            release.download('owner/repo', '26.10.11-RC2', Path('assets'))

    def test_apk_inventory_uses_only_release_runtime_modules(self):
        with tempfile.TemporaryDirectory() as tmp:
            lock = Path(tmp) / 'app-gradle.lockfile'
            lock.write_text('example.group:library:1.0=releaseRuntimeClasspath\nempty=\n')
            self.assertEqual(inventory.android_components(lock)[0]['purl'], 'pkg:maven/example.group/library@1.0')
            lock.write_text('example.group:library:1.0=debugRuntimeClasspath\n')
            with self.assertRaises(ValueError):
                inventory.android_components(lock)


if __name__ == '__main__':
    unittest.main()
