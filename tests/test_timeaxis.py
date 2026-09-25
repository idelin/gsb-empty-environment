import unittest
from datetime import datetime, timedelta, timezone
from fractions import Fraction

from temporal import Duration, OffsetTable, TimePoint, WallClock


def wc(hour=0, minute=0, second=0, microsecond=0):
    return WallClock(datetime(2026, 9, 26, hour, minute, second, microsecond,
                              tzinfo=timezone.utc))


class TestDistinctTypes(unittest.TestCase):
    def test_wallclock_is_not_a_timepoint(self):
        with self.assertRaises(TypeError):
            wc() < TimePoint(0)
        with self.assertRaises(TypeError):
            TimePoint(0) <= wc()

    def test_duration_is_not_a_timepoint(self):
        with self.assertRaises(TypeError):
            Duration(1) < TimePoint(1)

    def test_naive_wallclock_rejected(self):
        with self.assertRaises(ValueError):
            WallClock(datetime(2026, 9, 26))

    def test_exact_arithmetic(self):
        third = Duration(Fraction(1, 3))
        total = third + third + third
        self.assertEqual(total, Duration(1))
        start = TimePoint(Fraction(1, 3))
        end = start + Duration(Fraction(2, 3))
        self.assertEqual(end, TimePoint(1))
        self.assertEqual(end - start, Duration(Fraction(2, 3)))
        self.assertEqual(end - Duration(Fraction(1, 3)), TimePoint(Fraction(2, 3)))


class TestOffsetTable(unittest.TestCase):
    def setUp(self):
        # Synthetic table: 2 ticks/sec until 01:00, then 3/2 ticks/sec.
        self.table = OffsetTable([
            (wc(0), TimePoint(0), 2),
            (wc(1), TimePoint(7200), Fraction(3, 2)),
        ])

    def test_exact_conversion_before_second_anchor(self):
        tp = self.table.to_time_point(wc(0, 30))
        self.assertEqual(tp, TimePoint(3600))

    def test_exact_conversion_after_rate_change(self):
        tp = self.table.to_time_point(wc(1, 0, 2))
        self.assertEqual(tp, TimePoint(7203))

    def test_fractional_ticks_are_exact(self):
        tp = self.table.to_time_point(wc(1, 0, 0, 666667))
        self.assertIsInstance(tp.ticks, Fraction)
        self.assertEqual(tp.ticks, 7200 + Fraction(2000001, 2000000))

    def test_roundtrip(self):
        original = wc(1, 23, 45, 123456)
        tp = self.table.to_time_point(original)
        self.assertEqual(self.table.to_wall_clock(tp), original)

    def test_unrepresentable_microseconds_raise(self):
        # 1 tick at rate 2 ticks/sec = 0.5 s -> representable; 1/3 tick is not.
        with self.assertRaises(ValueError):
            self.table.to_wall_clock(TimePoint(Fraction(1, 3)))

    def test_anchor_validation(self):
        with self.assertRaises(ValueError):
            OffsetTable([])
        with self.assertRaises(ValueError):
            OffsetTable([(wc(1), TimePoint(0), 1), (wc(0), TimePoint(1), 1)]
                        )  # decreasing wall clock after sort -> points decrease
        with self.assertRaises(ValueError):
            OffsetTable([(wc(0), TimePoint(0), 0)])


if __name__ == "__main__":
    unittest.main()
