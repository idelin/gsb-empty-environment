"""DynamicConvexHull 的 unittest 测试套件。"""

import unittest
from fractions import Fraction

from convex_hull import DynamicConvexHull, HullVertex


def coords(hull):
    return [(v.x, v.y) for v in hull]


class TestDegenerate(unittest.TestCase):
    def test_empty(self):
        h = DynamicConvexHull()
        self.assertEqual(h.hull(), [])
        self.assertEqual(len(h), 0)
        with self.assertRaises(ValueError):
            h.extreme(1, 0)

    def test_single_point(self):
        h = DynamicConvexHull()
        h.add("a", 3, 4)
        hull = h.hull()
        self.assertEqual(coords(hull), [(Fraction(3), Fraction(4))])
        self.assertEqual(hull[0].ids, ("a",))
        r = h.extreme(-1, 0)
        self.assertEqual(r.value, Fraction(-3))

    def test_two_points(self):
        h = DynamicConvexHull()
        h.add("a", 0, 0)
        h.add("b", 5, 5)
        self.assertEqual(coords(h.hull()), [(Fraction(0), Fraction(0)), (Fraction(5), Fraction(5))])

    def test_all_collinear_is_ordered_chain(self):
        h = DynamicConvexHull()
        for i, x in enumerate([4, 1, 3, 0, 2]):  # 乱序插入
            h.add(i, x, 2 * x)
        chain = coords(h.hull())
        self.assertEqual(chain, [(Fraction(x), Fraction(2 * x)) for x in range(5)])

    def test_all_collinear_after_deletion(self):
        h = DynamicConvexHull()
        h.add("a", 0, 0)
        h.add("b", 1, 0)
        h.add("c", 2, 0)
        h.add("d", 1, 1)
        h.remove("d")
        self.assertEqual(coords(h.hull()), [(Fraction(0), Fraction(0)), (Fraction(1), Fraction(0)), (Fraction(2), Fraction(0))])


class TestCollinearPolicy(unittest.TestCase):
    def test_edge_collinear_points_retained_in_order(self):
        h = DynamicConvexHull()
        h.add("bl", 0, 0)
        h.add("mid", 1, 0)   # 底边共线点
        h.add("br", 2, 0)
        h.add("top", 1, 2)
        self.assertEqual(
            coords(h.hull()),
            [(Fraction(0), Fraction(0)), (Fraction(1), Fraction(0)), (Fraction(2), Fraction(0)), (Fraction(1), Fraction(2))],
        )

    def test_vertical_edge_collinear_retained(self):
        h = DynamicConvexHull()
        h.add("a", 0, 0)
        h.add("b", 0, Fraction(1, 2))
        h.add("c", 0, 1)
        h.add("d", 1, 0)
        h.add("e", 1, 1)
        self.assertEqual(
            coords(h.hull()),
            [(Fraction(0), Fraction(0)), (Fraction(1), Fraction(0)), (Fraction(1), Fraction(1)),
             (Fraction(0), Fraction(1)), (Fraction(0), Fraction(1, 2))],
        )

    def test_interior_point_does_not_change_envelope(self):
        h = DynamicConvexHull()
        for pid, x, y in [("a", 0, 0), ("b", 4, 0), ("c", 4, 4), ("d", 0, 4)]:
            h.add(pid, x, y)
        before = coords(h.hull())
        h.add("inner", 2, 2)
        h.add("inner2", Fraction(1, 3), Fraction(7, 5))
        self.assertEqual(coords(h.hull()), before)
        h.remove("inner")
        h.remove("inner2")
        self.assertEqual(coords(h.hull()), before)


class TestDuplicateIds(unittest.TestCase):
    def test_multiple_ids_same_coordinate(self):
        h = DynamicConvexHull()
        h.add("a1", 0, 0)
        h.add("a2", 0, 0)
        h.add("b", 1, 0)
        h.add("c", 0, 1)
        hull = h.hull()
        self.assertEqual(hull[0].ids, ("a1", "a2"))
        self.assertEqual(len(h), 4)
        self.assertEqual(h.num_distinct_points(), 3)

    def test_remove_one_id_keeps_geometric_point(self):
        h = DynamicConvexHull()
        h.add("a1", 0, 0)
        h.add("a2", 0, 0)
        h.add("b", 1, 0)
        h.add("c", 0, 1)
        self.assertTrue(h.remove("a1"))
        hull = h.hull()
        self.assertEqual(len(hull), 3)  # 几何点 (0,0) 仍在包络上
        self.assertEqual(hull[0].ids, ("a2",))
        self.assertEqual(h.ids_at(0, 0), ("a2",))

    def test_remove_last_id_removes_point(self):
        h = DynamicConvexHull()
        h.add("a", 0, 0)
        h.add("b", 1, 0)
        h.add("c", 0, 1)
        h.remove("a")
        self.assertEqual(coords(h.hull()), [(Fraction(0), Fraction(1)), (Fraction(1), Fraction(0))])
        self.assertEqual(h.ids_at(0, 0), ())

    def test_remove_missing_id_returns_false(self):
        h = DynamicConvexHull()
        self.assertFalse(h.remove("nope"))

    def test_duplicate_id_rejected(self):
        h = DynamicConvexHull()
        h.add("a", 0, 0)
        with self.assertRaises(ValueError):
            h.add("a", 1, 1)


class TestExactArithmetic(unittest.TestCase):
    def test_rational_input_forms(self):
        h = DynamicConvexHull()
        h.add("frac", Fraction(1, 3), Fraction(2, 3))
        h.add("tuple", (1, 2), (3, 2))  # (num, den) 元组
        h.add("int", 2, 0)
        hull = h.hull()
        for v in hull:
            self.assertIsInstance(v.x, Fraction)
            self.assertIsInstance(v.y, Fraction)

    def test_float_rejected(self):
        h = DynamicConvexHull()
        with self.assertRaises(TypeError):
            h.add("f", 0.5, 0)

    def test_near_collinear_no_epsilon(self):
        # C 在直线 AB 上方仅 2e-18：浮点/epsilon 会误判为共线，精确算术必须保留 C。
        h = DynamicConvexHull()
        h.add("a", 0, 0)
        h.add("b", 2, 1)
        h.add("c", 1, Fraction(1, 2) + Fraction(1, 10**18))
        self.assertEqual(
            coords(h.hull()),
            [(Fraction(0), Fraction(0)), (Fraction(2), Fraction(1)),
             (Fraction(1), Fraction(1, 2) + Fraction(1, 10**18))],
        )

    def test_exactly_collinear_rational_stays_on_edge(self):
        h = DynamicConvexHull()
        h.add("a", 0, 0)
        h.add("b", 3, 1)
        h.add("c", 6, 2)  # 严格共线
        self.assertEqual(
            coords(h.hull()),
            [(Fraction(0), Fraction(0)), (Fraction(3), Fraction(1)), (Fraction(6), Fraction(2))],
        )


class TestExtreme(unittest.TestCase):
    def setUp(self):
        self.h = DynamicConvexHull()
        for pid, x, y in [("bl", 0, 0), ("br", 2, 0), ("tr", 2, 2), ("tl", 0, 2)]:
            self.h.add(pid, x, y)

    def test_unique_max(self):
        r = self.h.extreme(1, 1)
        self.assertEqual(r.value, Fraction(4))
        self.assertEqual([v.ids for v in r.vertices], [("tr",)])

    def test_tie_returns_whole_edge(self):
        r = self.h.extreme(0, 1)  # 方向垂直于顶边 -> 两个角平局
        self.assertEqual(r.value, Fraction(2))
        self.assertEqual(sorted(i for v in r.vertices for i in v.ids), ["tl", "tr"])

    def test_tie_includes_collinear_edge_points(self):
        self.h.add("mid_top", 1, 2)  # 顶边上的共线点
        r = self.h.extreme(0, 1)
        self.assertEqual(sorted(i for v in r.vertices for i in v.ids), ["mid_top", "tl", "tr"])

    def test_rational_direction(self):
        r = self.h.extreme(Fraction(1, 2), Fraction(1, 3))
        self.assertEqual(r.value, Fraction(5, 3))
        self.assertEqual([v.ids for v in r.vertices], [("tr",)])

    def test_zero_direction_rejected(self):
        with self.assertRaises(ValueError):
            self.h.extreme(0, 0)


class TestDynamicUpdates(unittest.TestCase):
    def test_hull_updates_after_removals(self):
        h = DynamicConvexHull()
        h.add("a", 0, 0)
        h.add("b", 4, 0)
        h.add("c", 4, 4)
        h.add("d", 0, 4)
        h.add("peak", 2, 10)
        self.assertEqual(len(h.hull()), 5)
        h.remove("peak")
        self.assertEqual(
            coords(h.hull()),
            [(Fraction(0), Fraction(0)), (Fraction(4), Fraction(0)),
             (Fraction(4), Fraction(4)), (Fraction(0), Fraction(4))],
        )

    def test_cache_consistency_across_mixed_ops(self):
        h = DynamicConvexHull()
        for i in range(10):
            h.add(i, i, i * i % 7)
        first = coords(h.hull())
        self.assertEqual(first, coords(h.hull()))  # 缓存命中结果一致
        h.remove(3)
        h.add("extra", 5, 100)
        hull = coords(h.hull())
        self.assertIn((Fraction(5), Fraction(100)), hull)
        self.assertNotEqual(first, hull)


if __name__ == "__main__":
    unittest.main(verbosity=2)
