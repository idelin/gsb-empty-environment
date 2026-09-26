import unittest
from fractions import Fraction

from logmerge import (
    Duration,
    OffsetInterval,
    OffsetTable,
    TimePoint,
    WallClock,
)


class TestTimeTypes(unittest.TestCase):
    def test_exact_arithmetic(self):
        a = WallClock(Fraction("0.1"), "s")
        b = WallClock(Fraction("0.2"), "s")
        self.assertEqual((b - a).amount, Fraction("0.1"))  # exact, not 0.1 float

    def test_wallclock_plus_duration_gives_timepoint(self):
        t = WallClock(100, "s") + Duration(5)
        self.assertIsInstance(t, TimePoint)
        self.assertEqual(t.at, 105)

    def test_timepoint_minus_timepoint_gives_duration(self):
        d = TimePoint(10) - TimePoint(4)
        self.assertIsInstance(d, Duration)
        self.assertEqual(d.amount, 6)

    def test_illegal_mixes_rejected(self):
        with self.assertRaises(TypeError):
            WallClock(1, "a") + WallClock(2, "a")
        with self.assertRaises(TypeError):
            TimePoint(1) + TimePoint(2)
        with self.assertRaises(TypeError):
            WallClock(1, "a") - WallClock(2, "b")  # different sources
        with self.assertRaises(TypeError):
            Duration(1) + WallClock(1, "a")

    def test_offset_interval_validation(self):
        with self.assertRaises(ValueError):
            OffsetInterval(5, -5)

    def test_offset_table_convert(self):
        table = OffsetTable({"s": OffsetInterval(-2, 3)})
        lo, hi = table.convert(WallClock(10, "s"))
        self.assertEqual((lo.at, hi.at), (8, 13))
        with self.assertRaises(ValueError):
            table.interval("missing")


if __name__ == "__main__":
    unittest.main()
