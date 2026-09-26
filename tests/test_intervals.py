import unittest
from fractions import Fraction

from timemap import Interval, IntervalSet


class TestInterval(unittest.TestCase):
    def test_open_closed_contains(self):
        iv = Interval(0, 5, lo_open=False, hi_open=True)
        self.assertTrue(iv.contains(0))
        self.assertFalse(iv.contains(5))
        self.assertTrue(iv.contains(Fraction(5) - Fraction(1, 10**9)))

    def test_point_interval(self):
        p = Interval.point(3)
        self.assertTrue(p.is_point)
        self.assertTrue(p.contains(3))
        self.assertFalse(p.contains(Fraction(3) + Fraction(1, 1000)))

    def test_intersect_preserves_openness(self):
        a = Interval(0, 10, hi_open=False)
        b = Interval(5, 20, lo_open=True)
        hit = a.intersect(b)
        self.assertEqual((hit.lo, hit.hi, hit.lo_open, hit.hi_open),
                         (Fraction(5), Fraction(10), True, False))

    def test_intersect_touching_open_is_empty(self):
        a = Interval(0, 5, hi_open=True)
        b = Interval(5, 10, lo_open=True)
        self.assertIsNone(a.intersect(b))

    def test_intersect_touching_one_side_open_is_empty(self):
        a = Interval(0, 5, hi_open=False)
        b = Interval(5, 10, lo_open=True)
        self.assertIsNone(a.intersect(b))

    def test_intersect_touching_both_closed_is_point(self):
        a = Interval(0, 5, hi_open=False)
        b = Interval(5, 10, lo_open=False)
        hit = a.intersect(b)
        self.assertTrue(hit.is_point)
        self.assertEqual(hit.lo, 5)

    def test_merge_when_point_owned(self):
        s = IntervalSet([Interval(0, 5, hi_open=True), Interval(5, 10, hi_open=True)])
        self.assertEqual(len(s), 1)
        self.assertEqual(str(s), "[0, 10)")

    def test_no_merge_when_point_dropped(self):
        s = IntervalSet([Interval(0, 5, hi_open=True), Interval(5, 10, lo_open=True)])
        self.assertEqual(len(s), 2)

    def test_point_bridges_gap(self):
        s = IntervalSet([Interval(0, 5, hi_open=True), Interval.point(5),
                         Interval(5, 10, lo_open=True, hi_open=False)])
        self.assertEqual(str(s), "[0, 10]")

    def test_huge_intervals_are_breakpoint_based(self):
        big = 10**15
        s = IntervalSet([Interval(0, big), Interval(big, 2 * big, hi_open=False)])
        self.assertEqual(len(s), 1)
        self.assertIn(Fraction(10**15) * 3 // 2, s)
        self.assertNotIn(2 * big + 1, s)


if __name__ == "__main__":
    unittest.main()
