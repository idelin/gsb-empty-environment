"""精确有理 LP 求解器测试。"""

import unittest
from fractions import Fraction as F

from clockalign.lp import minimize_core, solve_lp


class TestSolveLp(unittest.TestCase):
    def test_basic_min(self):
        # min x+y s.t. x+2y>=4, x,y>=0 -> 值 2（顶点 (0,2)）
        r = solve_lp([F(1), F(1)], a_ub=[[F(-1), F(-2)]], b_ub=[F(-4)])
        self.assertTrue(r.feasible)
        self.assertEqual(r.value, F(2))

    def test_free_variable_max(self):
        r = solve_lp([F(1)], a_ub=[[F(1)]], b_ub=[F(5)], free_vars=[0], sense="max")
        self.assertTrue(r.feasible)
        self.assertEqual(r.value, F(5))
        self.assertEqual(r.x, [F(5)])

    def test_infeasible(self):
        r = solve_lp([F(1)], a_ub=[[F(-1)], [F(1)]], b_ub=[F(-2), F(1)])
        self.assertFalse(r.feasible)

    def test_negative_rhs_inequality(self):
        # x >= 0.999, x <= 1.001, x 自由 -> 可行
        r = solve_lp(
            [F(0)],
            a_ub=[[F(1)], [F(-1)]],
            b_ub=[F(1001, 1000), F(-999, 1000)],
            free_vars=[0],
        )
        self.assertTrue(r.feasible)
        self.assertGreaterEqual(r.x[0], F(999, 1000))
        self.assertLessEqual(r.x[0], F(1001, 1000))

    def test_equality(self):
        r = solve_lp([F(0), F(1)], a_eq=[[F(1), F(1)]], b_eq=[F(10)])
        self.assertTrue(r.feasible)
        self.assertEqual(r.value, F(0))

    def test_redundant_equality(self):
        # 两个相同等式（秩亏）+ 自由变量
        r = solve_lp(
            [F(0)] * 2,
            a_eq=[[F(1), F(1)], [F(2), F(2)]],
            b_eq=[F(4), F(8)],
            free_vars=[0, 1],
        )
        self.assertTrue(r.feasible)
        self.assertEqual(r.x[0] + r.x[1], F(4))

    def test_unbounded(self):
        # min -x, x >= -3, x 自由 -> 无界
        r = solve_lp([F(-1)], a_ub=[[F(-1)]], b_ub=[F(3)], free_vars=[0])
        self.assertTrue(r.feasible)
        self.assertTrue(r.unbounded)

    def test_exact_fraction_optimum(self):
        # min x s.t. 3x >= 1 -> 1/3
        r = solve_lp([F(1)], a_ub=[[F(-3)]], b_ub=[F(-1)])
        self.assertTrue(r.feasible)
        self.assertEqual(r.value, F(1, 3))


class TestMinimizeCore(unittest.TestCase):
    def test_shrinks_to_inclusion_minimal(self):
        # 不可行谓词：保留集合同时含 1 和 2 时不可行
        def is_feasible(kept):
            return not (1 in kept and 2 in kept)

        core = minimize_core(is_feasible, [0, 1, 2, 3])
        self.assertEqual(sorted(core), [1, 2])


if __name__ == "__main__":
    unittest.main()
