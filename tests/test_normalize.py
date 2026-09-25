import unittest
from fractions import Fraction

from chronodur import Duration, parse_duration


class TestCanonicalFormat(unittest.TestCase):
    def test_carry_within_fixed_units(self):
        # Proven semantics-preserving: 60s=1min, 60min=1h, 24h=1d exactly.
        self.assertEqual(str(parse_duration("90s")), "1min 30s")
        self.assertEqual(str(parse_duration("86400s")), "1d")
        self.assertEqual(str(parse_duration("90061s")), "1d 1h 1min 1s")
        self.assertEqual(str(parse_duration("25h")), "1d 1h")
        self.assertEqual(str(parse_duration("120min")), "2h")

    def test_no_carry_into_calendar_units(self):
        # Calendar and fixed domains never mix under normalization.
        self.assertEqual(str(parse_duration("24h")), "1d")    # fixed day: exact
        self.assertEqual(str(parse_duration("30cd")), "30cd")  # never months
        self.assertEqual(str(parse_duration("744h")), "31d")   # never 1mo
        self.assertEqual(str(parse_duration("1cd")), "1cd")    # never 24h
        self.assertEqual(str(parse_duration("12mo")), "12mo")  # never years

    def test_rational_seconds_print(self):
        self.assertEqual(str(parse_duration("0.5s")), "1/2s")
        self.assertEqual(str(parse_duration("1/3s")), "1/3s")
        self.assertEqual(str(parse_duration("2/4s")), "1/2s")

    def test_negative_duration_negates_whole(self):
        self.assertEqual(str(parse_duration("-1h30min")), "-(1h 30min)")
        self.assertEqual(str(parse_duration("-(1mo 2cd 3s)")), "-(1mo 2cd 3s)")

    def test_mixed_signs_use_explicit_operators(self):
        self.assertEqual(str(parse_duration("1mo - 3h")), "1mo - 3h")
        self.assertEqual(str(parse_duration("3h - 1mo")), "3h - 1mo")

    def test_zero(self):
        self.assertEqual(str(parse_duration("0s")), "0s")

    def test_round_trip_samples(self):
        samples = [
            "1mo", "2cd", "1d", "90s", "1/3s", "0.25h",
            "1mo 2cd 3d 4h 5min 6s", "-(1mo 30min)", "1mo - 3h",
            "0s", "1000000d", "7/8s", "1cd - 1s", "2*(3mo 4h)",
        ]
        for text in samples:
            with self.subTest(text=text):
                d1 = parse_duration(text)
                canonical = str(d1)
                d2 = parse_duration(canonical)
                self.assertEqual(d1, d2)
                self.assertEqual(canonical, str(d2))  # idempotent


class TestIdempotentNormalization(unittest.TestCase):
    def test_generated_values(self):
        for months in (-2, 0, 1, 13):
            for cal_days in (-5, 0, 40):
                for fixed in (Fraction(0), Fraction(1, 3),
                              Fraction(-90061), Fraction(86400 * 400)):
                    d = Duration(months, cal_days, fixed)
                    with self.subTest(d=d):
                        once = str(d)
                        twice = str(parse_duration(once))
                        self.assertEqual(once, twice)      # idempotent
                        self.assertEqual(parse_duration(once), d)  # round-trip


if __name__ == "__main__":
    unittest.main()
