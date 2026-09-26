import unittest

from interval_index import (
    INF,
    CONTAINED_BY,
    CONTAINS,
    INTERSECTS,
    Interval,
    IntervalError,
    contained_by,
    contains,
    intersects,
)


class NormalizationTest(unittest.TestCase):
    def test_half_open_integer_normalization(self):
        # [0,5) 在整数上是 {0,1,2,3,4}，[5,8) 是 {5,6,7}
        self.assertEqual((Interval.parse("[0,5)").lo,
                          Interval.parse("[0,5)").hi), (0, 4))
        self.assertEqual((Interval.parse("[5,8)").lo,
                          Interval.parse("[5,8)").hi), (5, 7))

    def test_open_endpoints_shift_by_one(self):
        self.assertEqual(Interval.parse("(0,5]").lo, 1)
        self.assertEqual(Interval.parse("[0,5)").hi, 4)
        self.assertEqual(Interval.parse("(0,5)"), Interval.parse("[1,4]"))

    def test_unbounded_endpoints(self):
        iv = Interval.parse("[3,)")
        self.assertEqual((iv.lo, iv.hi), (3, INF))
        iv = Interval.parse("(,0]")
        self.assertEqual((iv.lo, iv.hi), (-INF, 0))
        wide = Interval.parse("(,)")
        self.assertTrue(wide.lo == -INF and wide.hi == INF)

    def test_point_intervals_convention(self):
        point = Interval.parse("[5,5]")
        self.assertFalse(point.is_empty)
        self.assertTrue(point.covers(5))
        self.assertFalse(point.covers(4))
        # [x,x) 与 (x,x] 不含任何整数，归一化为空区间
        self.assertTrue(Interval.parse("[5,5)").is_empty)
        self.assertTrue(Interval.parse("(5,5]").is_empty)
        self.assertTrue(Interval.parse("(5,5)").is_empty)

    def test_missing_is_distinct_from_empty(self):
        missing = Interval.parse("")
        self.assertTrue(missing.is_missing)
        self.assertFalse(missing.is_empty)
        empty = Interval.parse("[5,4]")
        self.assertTrue(empty.is_empty)
        self.assertFalse(empty.is_missing)

    def test_invalid_syntax(self):
        for bad in ["1,5", "[1,5", "[a,b]", "[1;2]", "[1,2,3]"]:
            with self.subTest(bad=bad):
                with self.assertRaises(IntervalError):
                    Interval.parse(bad)


class RelationTest(unittest.TestCase):
    def test_adjacent_half_open_intervals_do_not_intersect(self):
        # 题目核心用例：[0,5) 与 [5,8) 不相交
        self.assertFalse(intersects(Interval.parse("[0,5)"),
                                    Interval.parse("[5,8)")))
        self.assertTrue(intersects(Interval.parse("[0,5]"),
                                   Interval.parse("[5,8)")))

    def test_point_intervals(self):
        point = Interval.parse("[5,5]")
        self.assertTrue(intersects(point, Interval.parse("[5,8)")))
        self.assertFalse(intersects(point, Interval.parse("[0,5)")))
        self.assertTrue(contains(Interval.parse("[0,10)"), point))
        self.assertFalse(contains(Interval.parse("[0,5)"), point))
        self.assertTrue(contained_by(point, Interval.parse("[0,10)")))

    def test_empty_and_missing_never_match(self):
        for other in ["[0,10]", "[5,5]", "(,)"]:
            q = Interval.parse(other)
            self.assertFalse(intersects(Interval.parse("[5,5)"), q))
            self.assertFalse(intersects(Interval.missing(), q))
            self.assertFalse(contains(Interval.parse("[5,5)"), q))
            self.assertFalse(contains(Interval.missing(), q))
            self.assertFalse(contained_by(Interval.parse("[5,5)"), q))

    def test_unbounded_relations(self):
        whole = Interval.parse("(,)")
        finite = Interval.parse("[-3,9]")
        self.assertTrue(intersects(whole, finite))
        self.assertTrue(contains(whole, finite))
        self.assertFalse(contains(finite, whole))
        self.assertTrue(contained_by(finite, whole))

    def test_contains_directions(self):
        outer, inner = Interval.parse("[0,10]"), Interval.parse("[2,8]")
        self.assertTrue(contains(outer, inner))
        self.assertFalse(contains(inner, outer))
        self.assertTrue(contained_by(inner, outer))


if __name__ == "__main__":
    unittest.main()
