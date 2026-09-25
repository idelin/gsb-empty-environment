import unittest
from datetime import date
from fractions import Fraction

from temporal import Duration, OffsetTable, TimePoint, WallClock


class DurationTest(unittest.TestCase):
    def test_exact_arithmetic(self):
        d = Duration.hours(1) + Duration.minutes(30)
        self.assertEqual(d.as_fraction, Fraction(5400))
        self.assertEqual(Duration(Fraction(1, 3)) * 3, Duration(1))
        self.assertEqual(Duration.days(2), Duration.hours(48))
        self.assertLess(Duration.seconds(1), Duration(Fraction(3, 2)))

    def test_duration_is_not_a_timepoint(self):
        with self.assertRaises(TypeError):
            Duration.seconds(1) + TimePoint(Duration.seconds(0))


class TimePointTest(unittest.TestCase):
    def test_arithmetic_and_type_distinction(self):
        t0 = TimePoint(Duration.seconds(100))
        t1 = t0 + Duration.seconds(50)
        self.assertIsInstance(t1, TimePoint)
        self.assertEqual((t1 - t0).as_fraction, Fraction(50))
        self.assertEqual((t1 - Duration.seconds(50)), t0)
        with self.assertRaises(TypeError):
            t0 + t1  # instant + instant is meaningless

    def test_ordering(self):
        a = TimePoint(Duration.seconds(1))
        b = TimePoint(Duration(Fraction(3, 2)))
        self.assertLess(a, b)


class OffsetTableTest(unittest.TestCase):
    def test_synthetic_offset_conversion(self):
        tab = OffsetTable()
        tab.define("UTC", Duration.hours(0))
        tab.derive("CST", "UTC", Duration.hours(8))
        tab.derive("SHIFT", "CST", Duration.minutes(30))  # synthetic composition
        self.assertEqual(tab.offset("SHIFT").as_fraction, Fraction(30600))

        wc = WallClock(date(2026, 9, 25), Duration.hours(9), "CST")
        tp = tab.to_timepoint(wc)
        days = (date(2026, 9, 25) - date(1970, 1, 1)).days
        # 09:00 at UTC+8 == 01:00 UTC exactly
        self.assertEqual(tp.offset.as_fraction, Fraction(days * 86400 + 3600))

        # 09:30 in the synthetic SHIFT zone (UTC+8:30) is the same instant
        wc2 = WallClock(date(2026, 9, 25), Duration.hours(9) + Duration.minutes(30), "SHIFT")
        self.assertEqual(tab.to_timepoint(wc2), tp)

    def test_unknown_zone(self):
        tab = OffsetTable()
        with self.assertRaises(KeyError):
            tab.offset("NOWHERE")


if __name__ == "__main__":
    unittest.main()
