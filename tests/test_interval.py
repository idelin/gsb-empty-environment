import unittest
from fractions import Fraction

from ratpiece.interval import Interval


class TestInterval(unittest.TestCase):
    def test_intersect_preserves_openness(self):
        a = Interval.closed_open(0, 3)      # [0, 3)
        b = Interval.open_closed(1, 3)      # (1, 3]
        c = a.intersect(b)                  # (1, 3)
        self.assertEqual((c.lo, c.hi), (Fraction(1), Fraction(3)))
        self.assertFalse(c.lo_closed)
        self.assertFalse(c.hi_closed)

    def test_intersect_closed_endpoint_kept(self):
        a = Interval.closed(0, 2)
        b = Interval.closed(2, 4)
        c = a.intersect(b)
        self.assertTrue(c.is_point())
        self.assertEqual(c.lo, Fraction(2))

    def test_intersect_open_touch_empty(self):
        a = Interval.open(0, 2)
        b = Interval.open(2, 4)
        self.assertIsNone(a.intersect(b))

    def test_contains_respects_flags(self):
        a = Interval.open(0, 1)
        self.assertFalse(a.contains(0))
        self.assertTrue(a.contains(Fraction(1, 2)))
        b = Interval.closed(0, 1)
        self.assertTrue(b.contains(0))

    def test_degenerate_open_rejected(self):
        with self.assertRaises(ValueError):
            Interval(Fraction(1), Fraction(1), False, True)

    def test_infinite_interval(self):
        a = Interval.all()
        self.assertTrue(a.contains(Fraction(-10**9)))


if __name__ == "__main__":
    unittest.main()

