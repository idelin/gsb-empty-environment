import unittest

from minisql import Interval, RangeScheme


class TestInterval(unittest.TestCase):
    def test_open_bounds_normalized_to_closed_integers(self):
        # (0, 10) 在整数域上即 [1, 9]
        iv = Interval.of(0, False, 10, False)
        self.assertEqual((iv.lo, iv.hi), (1, 9))
        self.assertFalse(iv.contains(0))
        self.assertTrue(iv.contains(1))
        self.assertTrue(iv.contains(9))
        self.assertFalse(iv.contains(10))

    def test_half_open(self):
        iv = Interval.of(0, True, 10, False)  # [0, 10) -> [0, 9]
        self.assertEqual((iv.lo, iv.hi), (0, 9))

    def test_empty_after_normalization(self):
        self.assertTrue(Interval.of(5, False, 6, False).is_empty)  # (5,6) 无整数

    def test_unbounded(self):
        iv = Interval.of(None, True, 10, True)
        self.assertTrue(iv.contains(-10**9))
        self.assertTrue(iv.contains(10))
        self.assertFalse(iv.contains(11))

    def test_intersection_boundary_exact(self):
        a = Interval.of(0, True, 10, False)   # [0,9]
        b = Interval.of(10, True, 20, False)  # [10,19]
        self.assertFalse(a.intersects(b))
        self.assertTrue(a.intersects(Interval(None, 9)))
        self.assertFalse(a.intersects(Interval(None, -1)))


class TestRangeCoverage(unittest.TestCase):
    def test_gap_rejected(self):
        with self.assertRaises(ValueError):
            RangeScheme([("a", Interval(None, 5)), ("b", Interval(7, None))])

    def test_overlap_rejected(self):
        with self.assertRaises(ValueError):
            RangeScheme([("a", Interval(None, 5)), ("b", Interval(5, None))])

    def test_must_cover_whole_line(self):
        with self.assertRaises(ValueError):
            RangeScheme([("a", Interval(0, 10)), ("b", Interval(11, None))])

    def test_adjacent_closed_bounds_ok(self):
        # [0,10] 与 (10,20] 归一化为 [0,10]、[11,20]，无缝衔接
        scheme = RangeScheme([
            ("lo", Interval.of(None, True, 0, False)),
            ("mid", Interval.of(0, True, 10, True)),
            ("hi", Interval.of(10, False, None, True)),
        ])
        self.assertEqual(scheme.route(-100), "lo")
        self.assertEqual(scheme.route(-1), "lo")
        self.assertEqual(scheme.route(0), "mid")
        self.assertEqual(scheme.route(10), "mid")
        self.assertEqual(scheme.route(11), "hi")
        self.assertEqual(scheme.route(10**9), "hi")


if __name__ == "__main__":
    unittest.main()
