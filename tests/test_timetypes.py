"""Type-level distinction between wall-clock points and durations."""

import unittest
from fractions import Fraction

from clockmap import Duration, WallClock


class TestTypeDistinctions(unittest.TestCase):
    def test_point_minus_point_is_duration(self):
        a = WallClock(Fraction(101, 2))
        b = WallClock(50)
        delta = a - b
        self.assertIsInstance(delta, Duration)
        self.assertEqual(delta.seconds, Fraction(1, 2))

    def test_point_plus_duration_is_point(self):
        t = WallClock(100) + Duration(Fraction(1, 3))
        self.assertIsInstance(t, WallClock)
        self.assertEqual(t.seconds, Fraction(301, 3))
        t2 = Duration(Fraction(1, 3)) + WallClock(100)
        self.assertEqual(t2, t)

    def test_point_plus_point_rejected(self):
        with self.assertRaises(TypeError):
            WallClock(1) + WallClock(2)

    def test_duration_arithmetic(self):
        d = Duration(Fraction(1, 4)) + Duration(Fraction(1, 4))
        self.assertEqual(d, Duration(Fraction(1, 2)))
        self.assertEqual(2 * Duration(3), Duration(6))
        self.assertEqual(Duration(7) / 2, Duration(Fraction(7, 2)))
        self.assertEqual(Duration(3) / Duration(2), Fraction(3, 2))

    def test_floats_rejected(self):
        with self.assertRaises(TypeError):
            WallClock(1.5)
        with self.assertRaises(TypeError):
            Duration(0.1)

    def test_ordering_and_equality(self):
        self.assertLess(WallClock(1), WallClock(2))
        self.assertEqual(WallClock(Fraction(1, 2)), WallClock(Fraction(2, 4)))
        self.assertNotEqual(Duration(1), WallClock(1))


class TestExactness(unittest.TestCase):
    def test_nearby_large_timestamps_not_merged(self):
        base = 10**15
        a = WallClock(base + Fraction(1, 10**6))
        b = WallClock(base + Fraction(2, 10**6))
        self.assertNotEqual(a, b)
        self.assertEqual((b - a).seconds, Fraction(1, 10**6))
        # Document the hazard this guards against: float64 cannot tell
        # these two instants apart (eps at 1e15 is ~0.125 s).
        self.assertEqual(float(a.seconds), float(b.seconds))


if __name__ == "__main__":
    unittest.main()
