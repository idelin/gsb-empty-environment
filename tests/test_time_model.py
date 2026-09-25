import unittest
from datetime import datetime, timezone
from fractions import Fraction

from trigger_sets import (
    SECOND,
    TICK,
    Duration,
    OffsetTable,
    TimePoint,
    WallClockTime,
)


class DurationTest(unittest.TestCase):
    def test_same_unit_arithmetic(self):
        self.assertEqual(Duration(3) + Duration(4), Duration(7, TICK))
        self.assertEqual(Duration(3, SECOND) - Duration(1, SECOND), Duration(2, SECOND))
        self.assertEqual(2 * Duration(Fraction(1, 2)), Duration(1, TICK))

    def test_mixed_units_rejected(self):
        with self.assertRaises(TypeError):
            Duration(1, TICK) + Duration(1, SECOND)


class TimePointTest(unittest.TestCase):
    def test_point_duration_arithmetic(self):
        self.assertEqual(TimePoint(5) + Duration(3), TimePoint(8))
        self.assertEqual(TimePoint(5) - Duration(3), TimePoint(2))
        self.assertEqual(TimePoint(5) - TimePoint(2), Duration(3, TICK))

    def test_point_plus_point_rejected(self):
        with self.assertRaises(TypeError):
            TimePoint(1) + TimePoint(2)

    def test_second_duration_rejected(self):
        with self.assertRaises(TypeError):
            TimePoint(1) + Duration(1, SECOND)

    def test_ordering(self):
        self.assertLess(TimePoint(3), TimePoint(Fraction(7, 2)))


class WallClockTimeTest(unittest.TestCase):
    def test_exact_seconds(self):
        moment = WallClockTime(Fraction(10)) + Duration(Fraction(1, 2), SECOND)
        self.assertEqual(moment.seconds, Fraction(21, 2))
        other = WallClockTime(Fraction(4))
        self.assertEqual(moment - other, Duration(Fraction(13, 2), SECOND))

    def test_tick_duration_rejected(self):
        with self.assertRaises(TypeError):
            WallClockTime(Fraction(10)) + Duration(1, TICK)

    def test_to_datetime_is_display_only(self):
        moment = WallClockTime(Fraction(60))
        self.assertEqual(
            moment.to_datetime(), datetime(1970, 1, 1, 0, 1, tzinfo=timezone.utc)
        )


class OffsetTableTest(unittest.TestCase):
    def table(self):
        return OffsetTable(
            [
                (0, 1_700_000_000, 1),                 # 1 wall-second per tick
                (1000, 1_700_001_000, Fraction(1, 2)),  # then half speed
            ]
        )

    def test_exact_conversion(self):
        table = self.table()
        self.assertEqual(
            table.to_wall(TimePoint(4)).seconds, Fraction(1_700_000_004)
        )
        self.assertEqual(
            table.to_wall(TimePoint(1002)).seconds, Fraction(1_700_001_001)
        )

    def test_round_trip(self):
        table = self.table()
        for tick in (0, 4, 999, 1000, 1234, 10**6):
            point = TimePoint(tick)
            self.assertEqual(table.to_timepoint(table.to_wall(point)), point)

    def test_discontinuous_table_rejected(self):
        with self.assertRaises(ValueError):
            OffsetTable([(0, 0, 1), (10, 999, 1)])

    def test_out_of_range_rejected(self):
        table = self.table()
        with self.assertRaises(ValueError):
            table.to_wall(TimePoint(-1))


if __name__ == "__main__":
    unittest.main()
