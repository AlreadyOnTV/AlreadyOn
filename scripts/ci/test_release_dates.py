"""Calendar and provenance boundaries for two-digit release naming."""
from datetime import date
import unittest
from release_dates import parse_day, release_day


class ReleaseDates(unittest.TestCase):
    def test_century_and_leap_days(self):
        self.assertEqual(parse_day('69.10.18'), date(2069, 10, 18))
        self.assertEqual(parse_day('00.02.29'), date(2000, 2, 29))
        self.assertEqual(release_day('2026-10-18', historical=True), '26.10.18')
        self.assertEqual(release_day('26-10-18', historical=True), '26.10.18')
        for invalid in ('26-10-18', '26.10-18', '2026-10-18', '26.02.29', '26.13.01', '26-10-1', '26.10.18-alpha1'):
            with self.assertRaises(ValueError):
                release_day(invalid)

    def test_historical_policy_has_no_century_downgrade(self):
        self.assertEqual(parse_day('2026-10-18', historical=True), parse_day('26.10.18'))
        with self.assertRaises(ValueError):
            parse_day('1926-10-18', historical=True)
