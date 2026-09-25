"""Core primitive tests: distinct time concepts and exact precision."""

import unittest

from tztable import Duration, Instant, WallClock
from tztable.core import NANOS_PER_DAY, NANOS_PER_HOUR


class TestDistinctConcepts(unittest.TestCase):
    def test_wall_minus_wall_is_duration(self):
        a = WallClock.of(2024, 1, 2, 12, 0, 0)
        b = WallClock.of(2024, 1, 1, 12, 0, 0)
        self.assertEqual(a - b, Duration.of(days=1))

    def test_wall_plus_duration_is_elapsed_not_calendar(self):
        start = WallClock.of(2024, 1, 31, 12, 0, 0)
        self.assertEqual(
            start.plus(Duration.of(hours=24)), WallClock.of(2024, 2, 1, 12, 0, 0)
        )
        self.assertEqual(start.plus_days(1), WallClock.of(2024, 2, 1, 12, 0, 0))
        # 24h == 1 calendar day here, but 30 * 24h != 1 calendar month:
        self.assertEqual(
            start.plus(Duration.of(hours=24 * 30)), WallClock.of(2024, 3, 1, 12, 0, 0)
        )
        self.assertEqual(start.plus_months(1), WallClock.of(2024, 2, 29, 12, 0, 0))

    def test_instant_plus_duration(self):
        instant = Instant.from_unix(1_700_000_000, 999)
        self.assertEqual(
            instant.plus(Duration.of(seconds=1)).epoch_nanos,
            1_700_000_001_000_000_999,
        )

    def test_wall_plus_wall_is_not_defined(self):
        with self.assertRaises(TypeError):
            WallClock.of(2024, 1, 1) + WallClock.of(2024, 1, 2)


class TestCalendarStepping(unittest.TestCase):
    def test_plus_months_clamps_day(self):
        self.assertEqual(
            WallClock.of(2024, 1, 31).plus_months(1), WallClock.of(2024, 2, 29)
        )
        self.assertEqual(
            WallClock.of(2023, 1, 31).plus_months(1), WallClock.of(2023, 2, 28)
        )

    def test_plus_years_leap_clamp(self):
        self.assertEqual(
            WallClock.of(2024, 2, 29).plus_years(1), WallClock.of(2025, 2, 28)
        )

    def test_plus_days_crosses_month_and_year(self):
        self.assertEqual(
            WallClock.of(2024, 12, 31, 23, 0).plus_days(1),
            WallClock.of(2025, 1, 1, 23, 0),
        )

    def test_invalid_dates_rejected(self):
        with self.assertRaises(ValueError):
            WallClock.of(2023, 2, 29)
        with self.assertRaises(ValueError):
            WallClock.of(2024, 13, 1)


class TestLargeEpochPrecision(unittest.TestCase):
    def test_nanosecond_round_trip_at_huge_epoch(self):
        nanos = 2**100 + 123_456_789
        instant = Instant(nanos)
        self.assertEqual(instant.to_wall().to_instant_equiv(), instant)

    def test_negative_epoch_round_trip(self):
        nanos = -(2**90) - 987_654_321
        instant = Instant(nanos)
        self.assertEqual(instant.to_wall().to_instant_equiv(), instant)

    def test_far_future_calendar(self):
        wall = WallClock.of(2_000_000, 2, 29, 23, 59, 59, 999_999_999)
        self.assertEqual(wall.to_ymd(), (2_000_000, 2, 29))
        self.assertEqual(wall.to_time(), (23, 59, 59, 999_999_999))
        with self.assertRaises(ValueError):
            WallClock.of(2_000_100, 2, 29)  # century, not divisible by 400

    def test_no_float_drift_in_duration_math(self):
        instant = Instant(10**30)
        stepped = instant.plus(Duration.of(nanoseconds=1))
        self.assertEqual(stepped.epoch_nanos - instant.epoch_nanos, 1)
        day, nod = divmod(10**30, NANOS_PER_DAY)
        self.assertEqual(instant.to_wall(), WallClock(day, nod))
        self.assertLess(0, NANOS_PER_HOUR)  # sanity on constants


if __name__ == "__main__":
    unittest.main()
