import unittest
from fractions import Fraction

from timemap import Duration, TimePoint, WallClock


class TestQuantities(unittest.TestCase):
    def test_exact_rational_arithmetic(self):
        self.assertEqual(
            (Duration("0.1") + Duration("0.2")).value, Fraction(3, 10)
        )

    def test_instant_minus_instant_is_duration(self):
        d = WallClock(10) - WallClock(4)
        self.assertIsInstance(d, Duration)
        self.assertEqual(d, Duration(6))

    def test_instant_plus_duration_keeps_type(self):
        self.assertEqual(WallClock(5) + Duration(3), WallClock(8))
        self.assertEqual(TimePoint(5) - Duration(2), TimePoint(3))

    def test_mixed_axes_rejected(self):
        with self.assertRaises(TypeError):
            WallClock(1) + WallClock(1)
        with self.assertRaises(TypeError):
            WallClock(1) - TimePoint(1)
        with self.assertRaises(TypeError):
            TimePoint(1) + TimePoint(1)
        with self.assertRaises(TypeError):
            WallClock(1) + 1

    def test_duration_scalar_ops(self):
        self.assertEqual(Duration(3) * 2, Duration(6))
        self.assertEqual(-Duration(3), Duration(-3))
        self.assertLess(Duration(1), Duration(2))


if __name__ == "__main__":
    unittest.main()
