import unittest
from fractions import Fraction

from timeindex import Duration, TimePoint, WallClockTime


class DurationTests(unittest.TestCase):
    def test_arithmetic_is_exact(self):
        self.assertEqual(Duration(1) + Duration(Fraction(1, 2)), Duration(Fraction(3, 2)))
        self.assertEqual(Duration(2) - Duration(3), Duration(-1))
        self.assertEqual(-Duration(Fraction(1, 3)), Duration(Fraction(-1, 3)))
        self.assertEqual(Duration(1) * 3, Duration(3))
        self.assertEqual(Duration(1) / Duration(Fraction(1, 2)), Fraction(2))

    def test_ordering(self):
        self.assertLess(Duration(-1), Duration(0))
        self.assertGreater(Duration(2), Duration(Fraction(3, 2)))


class TimePointTests(unittest.TestCase):
    def test_point_minus_point_is_duration(self):
        a = TimePoint("sensor", 5)
        b = TimePoint("sensor", Fraction(3, 2))
        self.assertEqual(a - b, Duration(Fraction(7, 2)))

    def test_point_plus_duration(self):
        got = TimePoint("sensor", 1) + Duration(Fraction(1, 3))
        self.assertEqual(got, TimePoint("sensor", Fraction(4, 3)))
        got = TimePoint("sensor", 1) - Duration(Fraction(1, 3))
        self.assertEqual(got, TimePoint("sensor", Fraction(2, 3)))

    def test_cannot_add_two_points(self):
        with self.assertRaises(TypeError):
            TimePoint("sensor", 1) + TimePoint("sensor", 2)

    def test_clock_mismatch_raises(self):
        with self.assertRaises(ValueError):
            TimePoint("a", 1) - TimePoint("b", 1)
        with self.assertRaises(ValueError):
            _ = TimePoint("a", 1) < TimePoint("b", 2)

    def test_wall_clock_is_a_distinct_concept(self):
        self.assertIsInstance(WallClockTime(0), TimePoint)
        self.assertEqual(WallClockTime(5) - WallClockTime(2), Duration(3))
        self.assertEqual(WallClockTime(2) + Duration(1), TimePoint("wall", 3))
        with self.assertRaises(ValueError):
            WallClockTime(1) - TimePoint("sensor", 1)

    def test_floats_are_rejected(self):
        with self.assertRaises(TypeError):
            TimePoint("sensor", 0.1)
        with self.assertRaises(TypeError):
            Duration(1.5)


if __name__ == "__main__":
    unittest.main()
