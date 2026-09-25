import unittest
from fractions import Fraction

from timestack import Duration, GapError, Instant, OffsetTable, WallClock


class TestExactArithmetic(unittest.TestCase):
    def test_duration_is_exact(self):
        third = Duration(Fraction(1, 3))
        total = third + third + third
        self.assertEqual(total, Duration(1))
        self.assertIsInstance(total.seconds, Fraction)

    def test_no_float_drift(self):
        d = Duration(Fraction(1, 10))
        acc = Duration(0)
        for _ in range(10):
            acc = acc + d
        self.assertEqual(acc, Duration(1))

    def test_duration_scaling_and_ratio(self):
        self.assertEqual(Duration(2) * 3, Duration(6))
        self.assertEqual(Duration(1) / Duration(4), Fraction(1, 4))
        self.assertEqual(Duration(3) / 2, Duration(Fraction(3, 2)))


class TestPointTypes(unittest.TestCase):
    def test_instant_arithmetic(self):
        t0 = Instant(10)
        self.assertEqual(t0 + Duration(5), Instant(15))
        self.assertEqual(t0 - Duration(5), Instant(5))
        self.assertEqual(Instant(15) - t0, Duration(5))

    def test_wallclock_arithmetic(self):
        w = WallClock(100)
        self.assertEqual(w + Duration(1), WallClock(101))
        self.assertEqual(WallClock(101) - w, Duration(1))

    def test_illegal_mixing_raises(self):
        with self.assertRaises(TypeError):
            Instant(1) + Instant(2)
        with self.assertRaises(TypeError):
            Instant(1) + WallClock(1)
        with self.assertRaises(TypeError):
            Instant(1) - WallClock(1)
        with self.assertRaises(TypeError):
            WallClock(1) + Instant(1)

    def test_points_of_different_types_are_never_equal(self):
        self.assertNotEqual(Instant(5), WallClock(5))
        self.assertFalse(Instant(5) == WallClock(5))


class TestOffsetTable(unittest.TestCase):
    def setUp(self):
        # Synthetic table: offset +2 from wall 0, jumping to +5 at wall 100.
        self.table = OffsetTable.synthetic(base_offset=2, steps=[(100, 5)])

    def test_wall_to_instant(self):
        self.assertEqual(self.table.to_instant(WallClock(10)), Instant(12))
        self.assertEqual(self.table.to_instant(WallClock(100)), Instant(105))

    def test_instant_to_wall(self):
        self.assertEqual(self.table.to_wall(Instant(12)), WallClock(10))
        self.assertEqual(self.table.to_wall(Instant(105)), WallClock(100))

    def test_roundtrip(self):
        for w in (0, 50, 99, 100, 250):
            wall = WallClock(w)
            self.assertEqual(self.table.to_wall(self.table.to_instant(wall)), wall)

    def test_gap_in_instant_space(self):
        # Offset increase 2 -> 5 at wall 100 skips instants [102, 105).
        with self.assertRaises(GapError):
            self.table.to_wall(Instant(103))
        self.assertEqual(self.table.to_wall(Instant(102 - 1)), WallClock(99))

    def test_ambiguous_table_rejected(self):
        with self.assertRaises(ValueError):
            OffsetTable.synthetic(base_offset=5, steps=[(100, 2)])

    def test_wall_predating_table_rejected(self):
        table = OffsetTable([(WallClock(10), Duration(0))])
        with self.assertRaises(GapError):
            table.to_instant(WallClock(5))


if __name__ == "__main__":
    unittest.main()
