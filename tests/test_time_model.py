import unittest
from datetime import datetime, timedelta, timezone
from fractions import Fraction

from priority_timeline import Duration, OffsetTable, TimePoint, WallClock

UTC = timezone.utc


def wc(hour=0, minute=0, second=0, microsecond=0):
    return WallClock(datetime(2026, 9, 25, hour, minute, second, microsecond, tzinfo=UTC))


class TestDuration(unittest.TestCase):
    def test_exact_arithmetic_no_float_error(self):
        third = Duration(Fraction(1, 3))
        self.assertEqual(third + third + third, Duration(1))
        self.assertEqual((third * 3).seconds, Fraction(1))

    def test_microseconds_exact(self):
        d = Duration.from_microseconds(Fraction(1, 3))
        self.assertEqual(d.microseconds, Fraction(1, 3))
        self.assertEqual(d.seconds, Fraction(1, 3_000_000))

    def test_type_safety(self):
        with self.assertRaises(TypeError):
            Duration(1) + 1  # noqa: B018
        with self.assertRaises(TypeError):
            Duration(1) < 2  # noqa: B018

    def test_float_rejected(self):
        with self.assertRaises(TypeError):
            Duration(0.1)


class TestTimePoint(unittest.TestCase):
    def test_exact_offset_arithmetic(self):
        a = TimePoint(Fraction(1, 3))
        b = a + Fraction(1, 6)
        self.assertEqual(b.offset, Fraction(1, 2))
        self.assertEqual(b - a, Fraction(1, 6))
        self.assertEqual((b - 1).offset, Fraction(-1, 2))

    def test_ordering(self):
        self.assertLess(TimePoint(3), TimePoint(7))
        self.assertLessEqual(TimePoint(3), TimePoint(3))

    def test_cannot_compare_with_wallclock(self):
        with self.assertRaises(TypeError):
            TimePoint(0) < wc()  # noqa: B018
        self.assertNotEqual(TimePoint(0), wc())

    def test_float_rejected(self):
        with self.assertRaises(TypeError):
            TimePoint(0.5)


class TestWallClock(unittest.TestCase):
    def test_requires_timezone(self):
        with self.assertRaises(ValueError):
            WallClock(datetime(2026, 9, 25, 8, 0, 0))

    def test_difference_is_exact_duration(self):
        a = wc(8, 0, 0)
        b = wc(9, 30, 15, 250000)
        self.assertEqual(b - a, Duration(Fraction(5415, 1) + Fraction(1, 4)))

    def test_add_duration(self):
        a = wc(8, 0, 0)
        self.assertEqual(a + Duration(90), wc(8, 1, 30))
        self.assertEqual(a - Duration(90), wc(7, 58, 30))

    def test_cannot_compare_with_timepoint(self):
        with self.assertRaises(TypeError):
            wc() < TimePoint(0)  # noqa: B018


class TestOffsetTable(unittest.TestCase):
    def setUp(self):
        # 合成轴：tick 0 -> 08:00，tick 100 -> 10:00（前 100 tick 每 tick 72s），
        # tick 200 -> 10:30（后 100 tick 每 tick 18s），非均匀映射。
        self.table = OffsetTable([
            (0, wc(8, 0, 0)),
            (100, wc(10, 0, 0)),
            (200, wc(10, 30, 0)),
        ])

    def test_anchor_points_exact(self):
        self.assertEqual(self.table.wallclock_at(TimePoint(0)), wc(8, 0, 0))
        self.assertEqual(self.table.wallclock_at(TimePoint(100)), wc(10, 0, 0))
        self.assertEqual(self.table.wallclock_at(TimePoint(200)), wc(10, 30, 0))

    def test_interpolation_exact_fraction(self):
        # tick 1/3 处：8:00 + (1/3)*72s = 8:00:24，微秒级精确
        us = self.table.wallclock_us_exact(TimePoint(Fraction(1, 3)))
        epoch_us = (wc(8, 0, 24).at - datetime(1970, 1, 1, tzinfo=UTC)) // timedelta(microseconds=1)
        self.assertEqual(us, epoch_us)
        # 第二段斜率不同：tick 150 -> 10:15
        self.assertEqual(self.table.wallclock_at(TimePoint(150)), wc(10, 15, 0))

    def test_inverse_roundtrip(self):
        tp = self.table.timepoint_at(wc(9, 0, 0))
        self.assertEqual(tp.offset, Fraction(50))
        self.assertEqual(self.table.wallclock_at(tp), wc(9, 0, 0))

    def test_extrapolation(self):
        # 表外沿最近段斜率外延：tick -50 -> 7:00（首段 72s/tick，50*72s=1h）
        self.assertEqual(self.table.wallclock_at(TimePoint(-50)), wc(7, 0, 0))
        # tick 250 -> 10:45（末段 18s/tick）
        self.assertEqual(self.table.wallclock_at(TimePoint(250)), wc(10, 45, 0))

    def test_duration_between_is_wallclock_duration(self):
        d = self.table.duration_between(TimePoint(0), TimePoint(150))
        self.assertEqual(d, Duration(2 * 3600 + 15 * 60))

    def test_advance(self):
        # 从 tick 100 推进 15 分钟墙钟 -> tick 150
        self.assertEqual(
            self.table.advance(TimePoint(100), Duration(15 * 60)),
            TimePoint(150),
        )

    def test_validation(self):
        with self.assertRaises(ValueError):
            OffsetTable([(0, wc(8))])
        with self.assertRaises(ValueError):
            OffsetTable([(0, wc(9)), (1, wc(8))])  # 墙钟倒流
        with self.assertRaises(ValueError):
            OffsetTable([(0, wc(8)), (0, wc(9))])  # 偏移重复


if __name__ == "__main__":
    unittest.main()
