import unittest
from datetime import datetime, timezone
from fractions import Fraction

from timefit.timebase import Duration, Instant, TimeTypeError, WallClock


class DurationTests(unittest.TestCase):
    def test_exact_arithmetic(self):
        d = Duration(milliseconds=1) + Duration(microseconds=1500)
        self.assertEqual(d, Duration(Fraction(1, 400)))

    def test_scalar_and_ratio(self):
        self.assertEqual(Duration(3) * Fraction(1, 2), Duration(milliseconds=1500))
        self.assertEqual(Duration(3) / Duration(2), Fraction(3, 2))
        self.assertEqual(Duration(3) / 2, Duration(Fraction(3, 2)))

    def test_rejects_float(self):
        with self.assertRaises(TimeTypeError):
            Duration(1.5)

    def test_ordering(self):
        self.assertLess(Duration(milliseconds=1), Duration(milliseconds=2))


class InstantTests(unittest.TestCase):
    def test_difference_is_duration(self):
        self.assertEqual(Instant(Fraction(25, 2)) - Instant(10), Duration(Fraction(5, 2)))

    def test_add_duration(self):
        self.assertEqual(Instant(10) + Duration(Fraction(1, 3)), Instant(Fraction(31, 3)))
        self.assertEqual(Duration(1) + Instant(10), Instant(11))

    def test_cannot_add_instants(self):
        with self.assertRaises(TimeTypeError):
            Instant(1) + Instant(2)

    def test_cannot_mix_wallclock_and_instant(self):
        wc = WallClock(datetime(2026, 1, 1, tzinfo=timezone.utc))
        with self.assertRaises(TimeTypeError):
            wc.to_instant() + wc
        with self.assertRaises(TimeTypeError):
            wc.to_instant() - wc


class WallClockTests(unittest.TestCase):
    def test_roundtrip_exact(self):
        wc = WallClock(datetime(2026, 3, 1, 12, 30, 15, 123456, tzinfo=timezone.utc))
        self.assertEqual(WallClock.from_instant(wc.to_instant()), wc)

    def test_naive_rejected(self):
        with self.assertRaises(TimeTypeError):
            WallClock(datetime(2026, 1, 1))

    def test_difference_is_duration(self):
        a = WallClock.from_iso("2026-01-01T00:00:00+00:00")
        b = WallClock.from_iso("2026-01-01T00:00:01.500000+00:00")
        self.assertEqual(b - a, Duration(milliseconds=1500))

    def test_add_duration(self):
        a = WallClock.from_iso("2026-01-01T00:00:00+00:00")
        self.assertEqual(
            a + Duration(milliseconds=1500),
            WallClock.from_iso("2026-01-01T00:00:01.500000+00:00"),
        )

    def test_non_microsecond_instant_rejected(self):
        with self.assertRaises(ValueError):
            WallClock.from_instant(Instant(Fraction(1, 3)))

    def test_normalised_to_utc(self):
        wc = WallClock.from_iso("2026-01-01T08:00:00+08:00")
        self.assertEqual(wc, WallClock.from_iso("2026-01-01T00:00:00+00:00"))


if __name__ == "__main__":
    unittest.main()
