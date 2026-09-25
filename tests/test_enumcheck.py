"""有理约束枚举对照测试：枚举顶点结果必须与单纯形 LP 完全一致。"""

import unittest
from fractions import Fraction as F

from clockalign import WallTime, correct, make_anchor, reconcile
from clockalign.enumcheck import enum_bounds, enumerate_vertices
from clockalign.lp import solve_lp
from clockalign.model import make_model
from clockalign.system import build_system, query_objective


def small_system():
    """2 段、连续交界、4 锚点的小系统。"""
    model = make_model([10], rate=(F(9, 10), F(11, 10)), continuous=True)
    anchors = [
        make_anchor(0, 100, "0.5"),
        make_anchor(5, 105, "0.5"),
        make_anchor(15, 115, "0.5"),
        make_anchor(20, 120, "0.5"),
    ]
    return model, anchors, build_system(model, anchors)


class TestEnumVsSimplex(unittest.TestCase):
    def test_vertex_enumeration_matches_lp_bounds(self):
        model, _anchors, system = small_system()
        vertices = enumerate_vertices(system)
        self.assertTrue(vertices)
        for x in (F(0), F(3), F(10), F(17), F(20)):
            seg = model.segment_of(x)
            obj = query_objective(model, seg, x)
            lo = solve_lp(
                obj, a_eq=system.a_eq, b_eq=system.b_eq,
                a_ub=system.a_ub, b_ub=system.b_ub,
                free_vars=system.free_vars, sense="min",
            )
            hi = solve_lp(
                obj, a_eq=system.a_eq, b_eq=system.b_eq,
                a_ub=system.a_ub, b_ub=system.b_ub,
                free_vars=system.free_vars, sense="max",
            )
            checked = enum_bounds(system, obj)
            self.assertIsNotNone(checked)
            self.assertEqual(checked, (lo.value, hi.value), f"x={x}")

    def test_correct_with_cross_check_enabled(self):
        model = make_model([10], rate=(F(9, 10), F(11, 10)), continuous=True)
        anchors = [
            make_anchor(0, 100, "0.5"),
            make_anchor(5, 105, "0.5"),
            make_anchor(15, 115, "0.5"),
            make_anchor(20, 120, "0.5"),
        ]
        recon = reconcile(model, anchors, cross_check=True)
        self.assertEqual(recon.min_removed, 0)
        # cross_check=True 时 correct 内部逐点与枚举对照（不一致会断言失败）
        for p in (0, 3, 10, 17, 20):
            c = correct(recon, WallTime(p))
            self.assertLessEqual(c.lower.seconds, c.upper.seconds)

    def test_enum_detects_infeasible(self):
        model = make_model([], rate=(1, 1))
        anchors = [
            make_anchor(0, 0, "0.1"),
            make_anchor(0, 5, "0.1"),  # 同本地时间、区间不相交
        ]
        system = build_system(model, anchors)
        self.assertEqual(enumerate_vertices(system), [])
        self.assertIsNone(enum_bounds(system, query_objective(model, 0, F(0))))


if __name__ == "__main__":
    unittest.main()
