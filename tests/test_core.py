import unittest
from fractions import Fraction

from timecal import (
    MICROSECOND,
    MILLISECOND,
    SECOND,
    UTC,
    ClockDomain,
    ClockKind,
    Duration,
    TimePoint,
    TimeUnit,
    WallClockTime,
)


class DurationTest(unittest.TestCase):
    def test_arithmetic_is_exact(self):
        tick = TimeUnit.sampling(44100)
        d = Duration.of(1, tick) + Duration.of(1, MICROSECOND)
        self.assertEqual(d.seconds, Fraction(1, 44100) + Fraction(1, 1_000_000))
        self.assertEqual((d - Duration.of(1, MICROSECOND)).seconds, Fraction(1, 44100))
        self.assertEqual((2 * Duration.of(3, MILLISECOND)).seconds, Fraction(6, 1000))

    def test_comparison(self):
        self.assertTrue(Duration.of(1, SECOND) > Duration.of(999, MILLISECOND))
        self.assertEqual(Duration.of(1000, MILLISECOND), Duration.of(1, SECOND))


class TimePointTest(unittest.TestCase):
    def test_point_duration_algebra(self):
        mono = ClockDomain("mono", ClockKind.MONOTONIC)
        t0 = TimePoint.at(mono, 5, SECOND)
        t1 = t0 + Duration.of(250, MILLISECOND)
        self.assertEqual((t1 - t0).seconds, Fraction(1, 4))
        self.assertEqual(t1 - Duration.of(250, MILLISECOND), t0)

    def test_domain_mismatch_rejected(self):
        mono = ClockDomain("mono", ClockKind.MONOTONIC)
        with self.assertRaises(ValueError):
            TimePoint.at(mono, 1) - TimePoint.at(UTC, 1)
        with self.assertRaises(ValueError):
            _ = TimePoint.at(mono, 1) < TimePoint.at(UTC, 2)

    def test_wall_clock_requires_wall_domain(self):
        mono = ClockDomain("mono", ClockKind.MONOTONIC)
        with self.assertRaises(ValueError):
            WallClockTime.at(mono, 0)


class LargeEpochTest(unittest.TestCase):
    def test_huge_epoch_plus_tiny_interval_loses_nothing(self):
        tick = TimeUnit.sampling(44100)
        epoch = 9_999_999_999_999_999
        t0 = WallClockTime.at(UTC, epoch, SECOND)
        t1 = t0 + Duration.of(1, tick)
        self.assertEqual(
            t1.since_epoch(SECOND), Fraction(epoch) + Fraction(1, 44100)
        )
        self.assertEqual(t1 - t0, Duration.of(1, tick))
        # the hazard we avoid: floats absorb the interval entirely
        self.assertEqual(float(epoch) + 1 / 44100, float(epoch))


if __name__ == "__main__":
    unittest.main()
