import unittest
from fractions import Fraction

from timeproj.types import Duration, Interval, TimePoint, WallClockTime


class TestDuration(unittest.TestCase):
    def test_exact_arithmetic(self):
        d = Duration(Fraction(1, 2)) + Duration(Fraction(1, 3))
        self.assertEqual(d.value, Fraction(5, 6))
        self.assertEqual((Duration(1) - Duration(Fraction(1, 4))).value,
                         Fraction(3, 4))
        self.assertEqual((2 * Duration(Fraction(1, 5))).value, Fraction(2, 5))
        self.assertEqual((-Duration(3)).value, Fraction(-3))
        self.assertEqual((Duration(1) / Duration(4)), Fraction(1, 4))


class TestTimePoint(unittest.TestCase):
    def test_arithmetic(self):
        t = TimePoint(10) + Duration(Fraction(1, 2))
        self.assertEqual(t.value, Fraction(21, 2))
        self.assertEqual((t - TimePoint(9)).value, Fraction(3, 2))
        self.assertEqual((t - Duration(Fraction(1, 2))).value, Fraction(10))

    def test_adding_two_timepoints_is_a_type_error(self):
        with self.assertRaises(TypeError):
            TimePoint(1) + TimePoint(2)


class TestWallClock(unittest.TestCase):
    def test_wall_clock_is_not_an_absolute_time(self):
        w = WallClockTime(100, clock="gps")
        with self.assertRaises(TypeError):
            w - TimePoint(100)
        with self.assertRaises(TypeError):
            TimePoint(100) - w

    def test_clock_identity_is_checked(self):
        w = WallClockTime(100, clock="gps")
        with self.assertRaises(TypeError):
            w - WallClockTime(99, clock="utc")
        self.assertEqual((w - WallClockTime(99, clock="gps")).value,
                         Fraction(1))
        self.assertEqual((w + Duration(1)).value, Fraction(101))


class TestInterval(unittest.TestCase):
    def test_contains_respects_openness(self):
        closed = Interval(TimePoint(0), TimePoint(1))
        self.assertTrue(closed.contains(TimePoint(0)))
        self.assertTrue(closed.contains(TimePoint(1)))
        half_open = Interval(TimePoint(0), TimePoint(1), end_open=True)
        self.assertTrue(half_open.contains(TimePoint(0)))
        self.assertFalse(half_open.contains(TimePoint(1)))

    def test_empty_interval_rejected(self):
        with self.assertRaises(ValueError):
            Interval(TimePoint(1), TimePoint(0))
        with self.assertRaises(ValueError):
            Interval(TimePoint(1), TimePoint(1), end_open=True)

    def test_intersection(self):
        a = Interval(TimePoint(0), TimePoint(2))
        b = Interval(TimePoint(1), TimePoint(3), start_open=True)
        inter = a.intersection(b)
        self.assertEqual(inter.start.value, Fraction(1))
        self.assertTrue(inter.start_open)
        self.assertEqual(inter.end.value, Fraction(2))
        self.assertIsNone(a.intersection(Interval(TimePoint(5), TimePoint(6))))

    def test_subtract_splits(self):
        a = Interval(TimePoint(0), TimePoint(10))
        b = Interval(TimePoint(3), TimePoint(5))
        parts = a.subtract(b)
        self.assertEqual(len(parts), 2)
        self.assertEqual(parts[0].end.value, Fraction(3))
        self.assertTrue(parts[0].end_open)  # 3 belongs to b
        self.assertEqual(parts[1].start.value, Fraction(5))
        self.assertTrue(parts[1].start_open)

    def test_subtract_open_interval_keeps_boundary_point(self):
        a = Interval(TimePoint(0), TimePoint(1))
        b = Interval(TimePoint(1), TimePoint(2), start_open=True)  # (1, 2]
        self.assertEqual(a.subtract(b), [a])  # 1 is not in b

    def test_subtract_point(self):
        a = Interval(TimePoint(0), TimePoint(2))
        point = Interval(TimePoint(1), TimePoint(1))
        parts = a.subtract(point)
        self.assertEqual(len(parts), 2)
        self.assertTrue(parts[0].end_open)
        self.assertTrue(parts[1].start_open)


if __name__ == "__main__":
    unittest.main()
