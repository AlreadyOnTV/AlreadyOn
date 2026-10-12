"""Check security report summaries and immutable scan target boundaries."""
import importlib.util
from pathlib import Path
import unittest
from datetime import date

spec = importlib.util.spec_from_file_location('security_scan', Path(__file__).parents[1] / 'security/scan.py')
scan = importlib.util.module_from_spec(spec)
spec.loader.exec_module(scan)


class SecurityScanTest(unittest.TestCase):
    def test_mutable_or_malformed_image_is_rejected(self):
        for image in ['example/image:nightly', 'example/image@sha256:123', '--password=secret',
                      'https://user:secret@example/image@sha256:' + 'a' * 64]:
            with self.assertRaises(ValueError):
                scan.validate_image(image)

    def test_exact_digest_is_accepted(self):
        image = 'ghcr.io/example/image@sha256:' + 'a' * 64
        self.assertEqual(scan.validate_image(image), image)

    def test_summary_retains_unknown_and_unfixed_without_leaking_matches(self):
        report = {'Results': [{'Vulnerabilities': [{'Severity': 'HIGH', 'Description': 'private'},
                                                  {'Severity': 'UNKNOWN'}, {'Severity': 'HIGH'}],
                               'Secrets': [{'Match': 'sensitive'}]}, {}]}
        self.assertEqual(scan.counts(report), ({'HIGH': 2, 'UNKNOWN': 1}, 1))

    def test_release_gate_blocks_unfixed_high_and_secret_matches(self):
        reports = {'image': {'Results': [{'Vulnerabilities': [{'Severity': 'HIGH'}]}]}}
        self.assertFalse(scan.evaluate_policy(reports, 'release')['passed'])
        self.assertTrue(scan.evaluate_policy(reports, 'report-only')['passed'])
        self.assertFalse(scan.evaluate_policy({'source': {'Results': [{'Secrets': [{'Match': 'private'}]}]}}, 'release')['passed'])
        self.assertTrue(scan.evaluate_policy({'source': {'Results': [{'Vulnerabilities': [{'Severity': 'MEDIUM'}]}]}}, 'release')['passed'])

    def test_exceptions_are_exact_scoped_and_time_limited(self):
        exception = {'scope': 'image', 'id': 'CVE-TEST', 'package': 'synthetic', 'version': '1',
                     'owner': 'maintainer', 'rationale': 'Synthetic fixture', 'tracking_issue': 'test', 'expires': '2026-10-20'}
        finding = {'Severity': 'HIGH', 'VulnerabilityID': 'CVE-TEST', 'PkgName': 'synthetic', 'InstalledVersion': '1'}
        reports = {'image': {'Results': [{'Vulnerabilities': [finding]}]}}
        self.assertTrue(scan.evaluate_policy(reports, 'release', [exception], date(2026, 10, 12))['passed'])
        self.assertFalse(scan.evaluate_policy({'source': reports['image']}, 'release', [exception], date(2026, 10, 12))['passed'])
        with self.assertRaisesRegex(ValueError, 'expire'):
            scan.evaluate_policy(reports, 'release', [exception], date(2026, 10, 21))
        with self.assertRaisesRegex(ValueError, 'Incomplete'):
            scan.evaluate_policy(reports, 'release', [{'expires': '2026-10-20'}])


if __name__ == '__main__':
    unittest.main()
