import unittest
from datetime import date
from fractions import Fraction

from calpart.exact import Duration, Instant, Interval, WallTime


class TestExactPrimitives(unittest.TestCase):
    def test_walltime_naive_seconds_roundtrip_fractional(self):
        w = WallTime(date(2023, 10, 29), Fraction(3, 2) * 3600 + Fraction(1, 3))
        back = WallTime.from_naive_seconds(w.to_naive_seconds())
        self.assertEqual(w, back)
        self.assertIsInstance(back.seconds, Fraction)

    def test_walltime_rejects_out_of_range(self):
        with self.assertRaises(ValueError):
            WallTime(date(2023, 1, 1), 86400)
        with self.assertRaises(ValueError):
            WallTime(date(2023, 1, 1), Fraction(-1, 2))

    def test_instant_duration_arithmetic(self):
        t0 = Instant(Fraction(1, 3))
        t1 = t0 + Duration.hours(Fraction(3, 2))
        self.assertEqual(t1.seconds, Fraction(1, 3) + 5400)
        delta = t1 - t0
        self.assertIsInstance(delta, Duration)
        self.assertEqual(delta.seconds, 5400)
        with self.assertRaises(TypeError):
            _ = t0 + t1  # instant + instant is meaningless

    def test_duration_sum_and_ratio(self):
        parts = [Duration(Fraction(1, 7)), Duration(Fraction(2, 7)), Duration(Fraction(4, 7))]
        self.assertEqual(sum(parts), Duration(1))
        self.assertEqual(Duration(2) / Duration(8), Fraction(1, 4))

    def test_interval_exact_length_and_intersection(self):
        a = Interval(Fraction(1, 3), Fraction(10, 3))
        b = Interval(Fraction(5, 3), 4)
        self.assertEqual(a.length, 3)
        inter = a.intersection(b)
        self.assertEqual((inter.start, inter.end), (Fraction(5, 3), Fraction(10, 3)))
        self.assertIsNone(a.intersection(Interval(4, 5)))
        self.assertFalse(a.contains(Fraction(10, 3)))  # half-open

    def test_interval_rejects_inverted(self):
        with self.assertRaises(ValueError):
            Interval(5, 4)


if __name__ == "__main__":
    unittest.main()
