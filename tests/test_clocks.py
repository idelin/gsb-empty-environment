import unittest
from fractions import Fraction

from temporal.clocks import Duration, OffsetTable, TimePoint, WallClockTime


class ClockKindsTest(unittest.TestCase):
    def test_exact_duration_arithmetic(self):
        d = Duration(Fraction(1, 3)) + Duration(Fraction(1, 6))
        self.assertEqual(d, Duration(Fraction(1, 2)))
        self.assertIsInstance(d.seconds, Fraction)

    def test_timepoint_minus_timepoint_is_duration(self):
        delta = TimePoint(10) - TimePoint(Fraction(7, 2))
        self.assertEqual(delta, Duration(Fraction(13, 2)))
        self.assertIsInstance(delta, Duration)

    def test_timepoint_plus_duration_is_timepoint(self):
        tp = TimePoint(5) + Duration(Fraction(3, 2))
        self.assertEqual(tp, TimePoint(Fraction(13, 2)))

    def test_no_point_plus_point(self):
        with self.assertRaises(TypeError):
            TimePoint(1) + TimePoint(2)

    def test_no_wallclock_minus_wallclock(self):
        a = WallClockTime("UTC", 10)
        b = WallClockTime("UTC", 3)
        with self.assertRaises(TypeError):
            a - b

    def test_wallclock_plus_duration_stays_wallclock(self):
        wc = WallClockTime("UTC", 10) + Duration(5)
        self.assertEqual(wc, WallClockTime("UTC", 15))

    def test_kinds_are_distinct(self):
        self.assertNotIsInstance(TimePoint(0), Duration)
        self.assertNotIsInstance(Duration(0), TimePoint)
        self.assertNotIsInstance(WallClockTime("UTC", 0), TimePoint)
        self.assertNotEqual(TimePoint(0), Duration(0))

    def test_exact_representation_no_floats(self):
        tp = TimePoint(Fraction(1, 3)) + Duration(Fraction(1, 3))
        self.assertEqual(tp.offset, Fraction(2, 3))
        self.assertIsInstance(tp.offset, Fraction)


class OffsetTableTest(unittest.TestCase):
    def setUp(self):
        self.table = OffsetTable.synthetic()

    def test_conversion_is_exact(self):
        wc = WallClockTime("THIRD", 10)
        tp = self.table.to_timepoint(wc)
        self.assertEqual(tp.offset, Fraction(10) - Fraction(1, 3))
        self.assertIsInstance(tp.offset, Fraction)

    def test_roundtrip(self):
        tp = TimePoint(Fraction(123, 7))
        wc = self.table.to_wallclock(tp, "PLUS2")
        self.assertEqual(self.table.to_timepoint(wc), tp)

    def test_cross_zone_duration(self):
        a = WallClockTime("PLUS2", 7200)            # epoch 0
        b = WallClockTime("MINUS5", -18000 + 60)    # epoch 60
        self.assertEqual(self.table.duration_between(a, b), Duration(60))

    def test_same_moment_different_readings(self):
        tp = TimePoint(3600)
        self.assertEqual(
            self.table.to_timepoint(self.table.to_wallclock(tp, "MINUS5")), tp
        )
        self.assertNotEqual(
            self.table.to_wallclock(tp, "PLUS2").reading,
            self.table.to_wallclock(tp, "MINUS5").reading,
        )

    def test_unknown_zone(self):
        with self.assertRaises(KeyError):
            self.table.to_timepoint(WallClockTime("NOPE", 0))


if __name__ == "__main__":
    unittest.main()
