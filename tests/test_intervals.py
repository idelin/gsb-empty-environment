import unittest

from timemap import Interval, IntervalSet


class TestInterval(unittest.TestCase):
    def test_reject_empty(self):
        with self.assertRaises(ValueError):
            Interval(5, 3)
        with self.assertRaises(ValueError):
            Interval(5, 5, True, False)  # 退化区间必须两端闭

    def test_maybe_returns_none_for_empty(self):
        self.assertIsNone(Interval.maybe(5, 5, True, False))
        self.assertIsNone(Interval.maybe(6, 5))

    def test_contains_respects_openness(self):
        iv = Interval(0, 10, True, False)
        self.assertTrue(iv.contains(0))
        self.assertFalse(iv.contains(10))
        self.assertTrue(iv.contains(5))

    def test_intersect_openness(self):
        a = Interval(0, 5, False, True)   # (0, 5]
        b = Interval(3, 9, True, False)   # [3, 9)
        self.assertEqual(a.intersect(b), Interval(3, 5, True, True))

    def test_intersect_touching_open_is_empty(self):
        a = Interval(0, 5, True, False)   # [0, 5)
        b = Interval(5, 9, True, True)    # [5, 9]
        self.assertIsNone(a.intersect(b))


class TestIntervalSet(unittest.TestCase):
    def test_merge_closed_touch(self):
        s = IntervalSet.of(Interval(0, 1, True, True), Interval(1, 2, False, True))
        self.assertEqual(s, IntervalSet.of(Interval(0, 2, True, True)))

    def test_no_merge_across_gap_point(self):
        s = IntervalSet.of(Interval(0, 1, True, False), Interval(1, 2, False, False))
        self.assertEqual(len(s), 2)  # 点 1 缺失，不可合并

    def test_merge_when_one_side_closed(self):
        s = IntervalSet.of(Interval(0, 1, True, False), Interval(1, 2, True, False))
        t = IntervalSet.of(Interval(0, 1, True, True), Interval(1, 2, False, False))
        self.assertEqual(s, IntervalSet.of(Interval(0, 2, True, False)))
        self.assertEqual(t, IntervalSet.of(Interval(0, 2, True, False)))

    def test_union_and_intersect(self):
        a = IntervalSet.of(Interval(0, 4), Interval(10, 14))
        b = IntervalSet.of(Interval(2, 12))
        self.assertEqual(
            a.intersect(b),
            IntervalSet.of(Interval(2, 4), Interval(10, 12)),
        )
        self.assertEqual(
            a.union(b),
            IntervalSet.of(Interval(0, 14)),
        )

    def test_point_interval(self):
        p = Interval.point(7)
        self.assertTrue(p.is_point)
        self.assertTrue(p.contains(7))
        self.assertEqual(
            IntervalSet.of(Interval(0, 7, True, False), p),
            IntervalSet.of(Interval(0, 7, True, True)),
        )


if __name__ == "__main__":
    unittest.main()
