"""DynamicConvexHull 的单元测试（标准库 unittest）。"""

import unittest
from fractions import Fraction

from convex_hull import DynamicConvexHull

F = Fraction


def coords(hull_with_ids):
    return [c for c, _ in hull_with_ids]


class DegenerateCasesTest(unittest.TestCase):
    def test_empty(self):
        h = DynamicConvexHull()
        self.assertEqual(h.hull(), [])
        self.assertEqual(len(h), 0)
        with self.assertRaises(ValueError):
            h.extreme(1, 0)

    def test_single_point(self):
        h = DynamicConvexHull()
        h.add("p", 3, -2)
        self.assertEqual(h.hull(), [(F(3), F(-2))])
        value, winners = h.extreme(1, 1)
        self.assertEqual(value, F(1))
        self.assertEqual(winners, [((F(3), F(-2)), ("p",))])

    def test_two_points_ordered(self):
        h = DynamicConvexHull()
        h.add("b", 5, 5)
        h.add("a", 1, 2)
        self.assertEqual(h.hull(), [(F(1), F(2)), (F(5), F(5))])

    def test_all_collinear_gives_ordered_chain(self):
        h = DynamicConvexHull()
        for i, pid in enumerate(["p3", "p0", "p2", "p1"]):
            h.add(pid, [3, 0, 2, 1][i], [6, 0, 4, 2][i])
        # 有序链：一端到另一端，含中间点，不往返重复
        self.assertEqual(
            h.hull(),
            [(F(0), F(0)), (F(1), F(2)), (F(2), F(4)), (F(3), F(6))],
        )

    def test_collinear_vertical_and_negative_slope(self):
        h = DynamicConvexHull()
        for pid, x, y in [("a", 2, 9), ("b", 2, 1), ("c", 2, 5)]:
            h.add(pid, x, y)
        self.assertEqual(h.hull(), [(F(2), F(1)), (F(2), F(5)), (F(2), F(9))])
        h2 = DynamicConvexHull()
        for pid, x, y in [("a", 0, 4), ("b", 4, 0), ("c", 2, 2), ("d", 1, 3)]:
            h2.add(pid, x, y)
        self.assertEqual(
            h2.hull(),
            [(F(0), F(4)), (F(1), F(3)), (F(2), F(2)), (F(4), F(0))],
        )

    def test_remove_down_to_degenerate(self):
        h = DynamicConvexHull()
        h.add("a", 0, 0)
        h.add("b", 1, 1)
        h.add("c", 0, 1)
        h.remove("c")
        self.assertEqual(h.hull(), [(F(0), F(0)), (F(1), F(1))])
        h.remove("b")
        self.assertEqual(h.hull(), [(F(0), F(0))])
        h.remove("a")
        self.assertEqual(h.hull(), [])


class DuplicateCoordTest(unittest.TestCase):
    def test_multiple_ids_one_coord(self):
        h = DynamicConvexHull()
        h.add("a", 0, 0)
        h.add("b", 0, 0)
        h.add("c", 2, 0)
        self.assertEqual(len(h), 3)
        self.assertEqual(h.ids_at(0, 0), ("a", "b"))
        self.assertEqual(h.hull(), [(F(0), F(0)), (F(2), F(0))])

    def test_remove_one_id_keeps_geometric_point(self):
        h = DynamicConvexHull()
        h.add("a", 0, 0)
        h.add("b", 0, 0)
        h.add("c", 2, 0)
        h.add("d", 1, 1)
        before = h.hull()
        h.remove("a")
        self.assertEqual(h.hull(), before)  # 几何点 (0,0) 仍在
        self.assertEqual(h.ids_at(0, 0), ("b",))
        h.remove("b")
        self.assertNotEqual(h.hull(), before)  # 最后一个 ID 删除，点才消失
        self.assertEqual(h.ids_at(0, 0), ())
        self.assertEqual(h.hull(), [(F(1), F(1)), (F(2), F(0))])

    def test_duplicate_id_rejected(self):
        h = DynamicConvexHull()
        h.add("a", 1, 1)
        with self.assertRaises(ValueError):
            h.add("a", 2, 2)

    def test_remove_missing_id(self):
        h = DynamicConvexHull()
        with self.assertRaises(ValueError):
            h.remove("nope")


class HullShapeTest(unittest.TestCase):
    def setUp(self):
        self.h = DynamicConvexHull()
        for pid, x, y in [
            ("A", 0, 0), ("B", 4, 0), ("C", 4, 4), ("D", 0, 4),
            ("E", 2, 0),   # 底边共线点
            ("G", 4, 2),   # 右边共线点
            ("F", 2, 2),   # 内部点
            ("H", 1, 2),   # 内部点
        ]:
            self.h.add(pid, x, y)

    def test_edge_collinear_points_kept_in_order(self):
        self.assertEqual(
            self.h.hull(),
            [
                (F(0), F(0)), (F(2), F(0)), (F(4), F(0)),
                (F(4), F(2)), (F(4), F(4)),
                (F(0), F(4)),
            ],
        )

    def test_ccw_orientation(self):
        hull = self.h.hull()
        twice_area = sum(
            hull[i][0] * hull[(i + 1) % len(hull)][1]
            - hull[(i + 1) % len(hull)][0] * hull[i][1]
            for i in range(len(hull))
        )
        self.assertGreater(twice_area, 0)

    def test_source_mapping(self):
        self.h.add("A2", 0, 0)
        mapping = dict(self.h.hull_with_ids())
        self.assertEqual(mapping[(F(0), F(0))], ("A", "A2"))
        self.assertEqual(mapping[(F(4), F(0))], ("B",))

    def test_interior_insert_does_not_change_envelope(self):
        before = self.h.hull()
        self.h.add("I", 3, 3)
        self.h.add("J", F(1, 2), F(5, 2))
        self.assertEqual(self.h.hull(), before)

    def test_delete_vertex_updates_hull(self):
        self.h.remove("C")
        self.assertEqual(
            self.h.hull(),
            [(F(0), F(0)), (F(2), F(0)), (F(4), F(0)), (F(4), F(2)), (F(0), F(4))],
        )


class ExactnessTest(unittest.TestCase):
    def test_rational_collinear_exact(self):
        # (0,0), (1,1/3), (3,1) 精确共线；浮点 1/3*3 != 1 会误判
        h = DynamicConvexHull()
        h.add("a", 0, 0)
        h.add("b", 1, F(1, 3))
        h.add("c", 3, 1)
        self.assertEqual(
            h.hull(), [(F(0), F(0)), (F(1), F(1, 3)), (F(3), F(1))]
        )

    def test_rational_near_miss_not_collinear(self):
        # 与上面只差 1/10^18 的点必须被判定为顶点（精确区分）
        h = DynamicConvexHull()
        h.add("a", 0, 0)
        h.add("b", 1, F(1, 3) + F(1, 10**18))
        h.add("c", 3, 1)
        h.add("d", 3, 0)
        hull = h.hull()
        self.assertIn((F(1), F(1, 3) + F(1, 10**18)), hull)

    def test_huge_integers_no_overflow(self):
        big = 10**30
        h = DynamicConvexHull()
        h.add("a", 0, 0)
        h.add("b", big, 1)
        h.add("c", big - 1, 0)
        h.add("d", big, big)
        # (big-1, 0) 在 (0,0)->(big,1) 边的下方，是顶点
        self.assertIn((F(big - 1), F(0)), h.hull())

    def test_float_rejected(self):
        h = DynamicConvexHull()
        with self.assertRaises(TypeError):
            h.add("a", 0.1, 0.2)
        with self.assertRaises(TypeError):
            h.add("b", True, 0)

    def test_extreme_exact_rational_value(self):
        h = DynamicConvexHull()
        h.add("a", F(1, 3), F(1, 7))
        h.add("b", 1, 0)
        value, winners = h.extreme(F(1, 2), F(2, 3))
        self.assertEqual(value, F(1, 2))
        self.assertEqual(winners[0][1], ("b",))


class ExtremeQueryTest(unittest.TestCase):
    def setUp(self):
        self.h = DynamicConvexHull()
        for pid, x, y in [
            ("A", 0, 0), ("E", 2, 0), ("B", 4, 0),
            ("C", 4, 4), ("D", 0, 4), ("F", 2, 2),
        ]:
            self.h.add(pid, x, y)

    def test_max_direction(self):
        value, winners = self.h.extreme(1, 1)
        self.assertEqual(value, F(8))
        self.assertEqual(winners, [((F(4), F(4)), ("C",))])

    def test_min_via_negated_direction(self):
        value, winners = self.h.extreme(-1, -1)
        self.assertEqual(value, F(0))
        self.assertEqual(winners, [((F(0), F(0)), ("A",))])

    def test_tie_along_edge_returns_all_collinear_points(self):
        value, winners = self.h.extreme(0, -1)
        self.assertEqual(value, F(0))
        self.assertEqual(
            winners,
            [
                ((F(0), F(0)), ("A",)),
                ((F(2), F(0)), ("E",)),
                ((F(4), F(0)), ("B",)),
            ],
        )

    def test_zero_direction_rejected(self):
        with self.assertRaises(ValueError):
            self.h.extreme(0, 0)

    def test_extreme_after_deletion(self):
        self.h.remove("C")
        value, winners = self.h.extreme(1, 1)
        self.assertEqual(value, F(4))
        # 删除 C 后 F(2,2) 恰落在 (4,0)-(0,4) 边上，按保留策略成为边界点，
        # 方向 (1,1) 的最大值沿整条边取得
        self.assertEqual(
            winners,
            [
                ((F(4), F(0)), ("B",)),
                ((F(2), F(2)), ("F",)),
                ((F(0), F(4)), ("D",)),
            ],
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
