import unittest
from datetime import datetime, timezone
from fractions import Fraction

from bitemporal import Duration, SyntheticOffsetTable, TimePoint, WallClockTime


def tp(seconds=0, micros=0):
    return TimePoint.at(seconds=seconds, micros=micros)


class DurationTest(unittest.TestCase):
    def test_exact_fractional_arithmetic(self):
        third = Duration.microseconds(Fraction(1, 3))
        total = third + third + third
        self.assertEqual(total, Duration.microseconds(1))

    def test_units(self):
        self.assertEqual(Duration.seconds(2), Duration.milliseconds(2000))
        self.assertEqual(Duration.minutes(1), Duration.seconds(60))
        self.assertEqual(Duration.hours(1), Duration.seconds(3600))
        self.assertEqual(Duration.seconds(3).total_seconds(), Fraction(3))

    def test_scaling_and_ratio(self):
        d = Duration.seconds(2)
        self.assertEqual(d * 3, Duration.seconds(6))
        self.assertEqual(3 * d, Duration.seconds(6))
        self.assertEqual(d / 4, Duration.milliseconds(500))
        self.assertEqual(d / Duration.seconds(1), Fraction(2))

    def test_ordering(self):
        self.assertLess(Duration.seconds(1), Duration.seconds(2))
        self.assertGreater(Duration.microseconds(1), Duration.microseconds(0))


class TimePointTest(unittest.TestCase):
    def test_point_plus_duration(self):
        self.assertEqual(tp(5) + Duration.seconds(2), tp(7))
        self.assertEqual(Duration.seconds(2) + tp(5), tp(7))
        self.assertEqual(tp(5) - Duration.seconds(2), tp(3))

    def test_point_minus_point_is_duration(self):
        delta = tp(9) - tp(4)
        self.assertIsInstance(delta, Duration)
        self.assertEqual(delta, Duration.seconds(5))

    def test_exact_fractional_points(self):
        a = TimePoint(Fraction(1, 3))
        b = a + Duration.microseconds(Fraction(2, 3))
        self.assertEqual(b, TimePoint(1))

    def test_point_plus_point_rejected(self):
        with self.assertRaises(TypeError):
            tp(1) + tp(2)

    def test_ordering(self):
        self.assertLess(tp(1), tp(2))
        self.assertEqual(tp(1), TimePoint.at(seconds=1))


class WallClockTest(unittest.TestCase):
    def test_datetime_roundtrip(self):
        dt = datetime(2024, 3, 1, 12, 30, 15, 123456, tzinfo=timezone.utc)
        wall = WallClockTime.from_datetime(dt)
        self.assertEqual(wall.to_datetime(), dt)

    def test_naive_datetime_rejected(self):
        with self.assertRaises(ValueError):
            WallClockTime.from_datetime(datetime(2024, 1, 1))

    def test_sub_microsecond_not_representable(self):
        wall = WallClockTime(Fraction(1, 2))
        with self.assertRaises(ValueError):
            wall.to_datetime()

    def test_wall_minus_wall_is_duration(self):
        a = WallClockTime.from_datetime(datetime(2024, 1, 2, tzinfo=timezone.utc))
        b = WallClockTime.from_datetime(datetime(2024, 1, 1, tzinfo=timezone.utc))
        self.assertEqual(a - b, Duration.hours(24))

    def test_wall_plus_duration(self):
        a = WallClockTime.from_datetime(datetime(2024, 1, 1, tzinfo=timezone.utc))
        self.assertEqual(
            (a + Duration.hours(24)).to_datetime(),
            datetime(2024, 1, 2, tzinfo=timezone.utc),
        )


class CrossTypeTest(unittest.TestCase):
    def test_timelines_do_not_mix(self):
        wall = WallClockTime.from_datetime(datetime(2024, 1, 1, tzinfo=timezone.utc))
        with self.assertRaises(TypeError):
            tp(1) + wall
        with self.assertRaises(TypeError):
            wall + tp(1)
        with self.assertRaises(TypeError):
            tp(1) - wall
        with self.assertRaises(TypeError):
            tp(1) < wall
        with self.assertRaises(TypeError):
            Duration.seconds(1) < tp(1)


class SyntheticOffsetTableTest(unittest.TestCase):
    def setUp(self):
        self.table = SyntheticOffsetTable()
        self.table.add_anchor(
            tp(0), WallClockTime.from_datetime(datetime(2024, 1, 1, tzinfo=timezone.utc))
        )

    def test_roundtrip_exact(self):
        point = tp(3600, micros=123)
        wall = self.table.to_wall_clock(point)
        self.assertEqual(self.table.to_time_point(wall), point)
        self.assertEqual(
            wall.to_datetime(), datetime(2024, 1, 1, 1, 0, 0, 123, tzinfo=timezone.utc)
        )

    def test_multiple_anchors_piecewise(self):
        self.table.add_anchor(
            tp(7200),
            WallClockTime.from_datetime(datetime(2024, 1, 1, 4, tzinfo=timezone.utc)),
        )
        # before second anchor: 1:1 from first anchor
        self.assertEqual(
            self.table.to_wall_clock(tp(3600)).to_datetime(),
            datetime(2024, 1, 1, 1, tzinfo=timezone.utc),
        )
        # at/after second anchor: 1:1 from second anchor (offset jumped)
        self.assertEqual(
            self.table.to_wall_clock(tp(7200)).to_datetime(),
            datetime(2024, 1, 1, 4, tzinfo=timezone.utc),
        )
        self.assertEqual(
            self.table.to_time_point(
                WallClockTime.from_datetime(datetime(2024, 1, 1, 5, tzinfo=timezone.utc))
            ),
            tp(10800),
        )

    def test_scaled_rate(self):
        table = SyntheticOffsetTable(rate=Fraction(2))  # wall runs 2x synthetic
        table.add_anchor(tp(0), WallClockTime(0))
        self.assertEqual(table.to_wall_clock(tp(1)), WallClockTime(2_000_000))
        self.assertEqual(table.to_time_point(WallClockTime(2_000_000)), tp(1))

    def test_fractional_rate_exact(self):
        table = SyntheticOffsetTable(rate=Fraction(1, 3))
        table.add_anchor(tp(0), WallClockTime(0))
        wall = table.to_wall_clock(tp(1))
        self.assertEqual(wall.micros, Fraction(1_000_000, 3))
        self.assertEqual(table.to_time_point(wall), tp(1))

    def test_duplicate_anchor_rejected(self):
        with self.assertRaises(ValueError):
            self.table.add_anchor(tp(0), WallClockTime(0))

    def test_empty_table_rejected(self):
        with self.assertRaises(ValueError):
            SyntheticOffsetTable().to_wall_clock(tp(0))

    def test_wrong_types_rejected(self):
        with self.assertRaises(TypeError):
            self.table.to_wall_clock(WallClockTime(0))
        with self.assertRaises(TypeError):
            self.table.to_time_point(tp(0))


if __name__ == "__main__":
    unittest.main()
