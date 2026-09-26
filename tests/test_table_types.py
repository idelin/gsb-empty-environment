import unittest
from fractions import Fraction

from timemap import (Duration, LogicalTime, OffsetTable, PiecewiseMap,
                     WallClock)


def make_map():
    return PiecewiseMap.build(0, 0, [(5, 1), (5, 0), (10, 2), (10, -1)])


class TestTypes(unittest.TestCase):
    def test_point_minus_point_is_duration(self):
        self.assertEqual(WallClock(10) - WallClock(4), Duration(6))
        self.assertEqual(LogicalTime(9) - LogicalTime(4), Duration(5))

    def test_point_plus_duration(self):
        self.assertEqual(WallClock(4) + Duration(6), WallClock(10))
        self.assertEqual(WallClock(10) - Duration(6), WallClock(4))

    def test_point_plus_point_rejected(self):
        with self.assertRaises(TypeError):
            WallClock(1) + WallClock(2)

    def test_wallclock_and_logical_are_distinct(self):
        self.assertNotEqual(WallClock(5), LogicalTime(5))

    def test_exact_representation(self):
        self.assertEqual(WallClock(Fraction(1, 3)).value, Fraction(1, 3))
        self.assertEqual(WallClock("0.1").value, Fraction(1, 10))
        with self.assertRaises(TypeError):
            WallClock(0.1)

    def test_duration_arithmetic(self):
        self.assertEqual(Duration(3) + Duration(4), Duration(7))
        self.assertEqual(2 * Duration(3), Duration(6))
        self.assertLess(Duration(1), Duration(2))


class TestOffsetTable(unittest.TestCase):
    def setUp(self):
        self.m = make_map()
        self.table = OffsetTable.from_map(self.m)

    def test_entries(self):
        self.assertEqual(len(self.table.entries), 4)
        e = self.table.entries[2]
        self.assertEqual((e.real_start, e.real_end, e.logical_start, e.rate),
                         (10, 20, 5, 2))

    def test_to_logical_matches_map(self):
        for t in (0, 5, 7, 10, 12, 20, 25, 30):
            self.assertEqual(self.table.to_logical(WallClock(t)),
                             LogicalTime(self.m.eval(t)))

    def test_to_real_returns_boundary_choices(self):
        pre = self.table.to_real(LogicalTime(5))
        self.assertEqual(str(pre), "[5, 10]")

    def test_exact_fraction_conversion(self):
        self.assertEqual(self.table.to_logical(WallClock(Fraction(25, 2))),
                         LogicalTime(10))


if __name__ == "__main__":
    unittest.main()
