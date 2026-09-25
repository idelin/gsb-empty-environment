import unittest
from fractions import Fraction

from bucketindex.timeaxis import (
    Duration,
    Instant,
    OffsetTable,
    WallClock,
    WallClockDelta,
)


class TestTimeTypes(unittest.TestCase):
    def test_instant_minus_instant_is_duration(self):
        d = Instant(30) - Instant(10)
        self.assertIsInstance(d, Duration)
        self.assertEqual(d.ticks, 20)

    def test_instant_plus_duration_is_instant(self):
        self.assertEqual(Instant(10) + Duration(5), Instant(15))
        self.assertEqual(Instant(10) - Duration(25), Instant(-15))

    def test_wallclock_minus_wallclock_is_not_duration(self):
        delta = WallClock(10) - WallClock(4)
        self.assertIsInstance(delta, WallClockDelta)
        self.assertNotIsInstance(delta, Duration)
        self.assertEqual(delta.span, Fraction(6))

    def test_cannot_mix_wallclock_and_instant(self):
        with self.assertRaises(TypeError):
            _ = WallClock(1) + Instant(1)


class TestOffsetTable(unittest.TestCase):
    def setUp(self):
        # Segment 0: wall [0, 100) at 10 ticks/unit -> ticks [0, 1000)
        # Segment 1: wall [100, ...) at 2 ticks/unit  -> ticks [1000, ...)
        self.table = OffsetTable([(0, 0, 10), (100, 1000, 2)])

    def test_exact_mapping_per_segment(self):
        self.assertEqual(self.table.wall_to_instant(WallClock(0)), Instant(0))
        self.assertEqual(self.table.wall_to_instant(WallClock(50)), Instant(500))
        self.assertEqual(self.table.wall_to_instant(WallClock(100)), Instant(1000))
        self.assertEqual(self.table.wall_to_instant(WallClock(150)), Instant(1100))

    def test_fractional_wall_reading_is_exact(self):
        exact = self.table.wall_to_ticks_exact(WallClock(Fraction(1, 3)))
        self.assertEqual(exact, Fraction(10, 3))
        # Flooring to whole ticks is exact integer arithmetic.
        self.assertEqual(
            self.table.wall_to_instant(WallClock(Fraction(1, 3))), Instant(3)
        )

    def test_roundtrip(self):
        for ticks in (0, 7, 999, 1000, 1234):
            wall = self.table.instant_to_wall(Instant(ticks))
            self.assertEqual(self.table.wall_to_instant(wall), Instant(ticks))

    def test_duration_between_uses_table(self):
        d = self.table.duration_between(WallClock(90), WallClock(110))
        # wall 90 -> tick 900, wall 110 -> tick 1020
        self.assertEqual(d, Duration(120))

    def test_invalid_table_rejected(self):
        with self.assertRaises(ValueError):
            OffsetTable([])
        with self.assertRaises(ValueError):
            OffsetTable([(10, 0, 1), (5, 100, 1)])  # not increasing
        with self.assertRaises(ValueError):
            OffsetTable([(0, 0, 1), (10, 5, 1)])  # ticks go backwards


if __name__ == "__main__":
    unittest.main()
