"""intprop 的 unittest 测试套件。

包含一个精确整数参考枚举器，对随机小实例逐值检验每次剪枝：
凡被传播剪掉（排除出当前域）的值，不得出现在任何参考解中。
"""

import random
import unittest
from fractions import Fraction
from math import ceil, floor

from intprop import (
    Solver,
    ceil_div,
    check_assignment,
    enumerate_solutions,
    floor_div,
)


class TestDivRounding(unittest.TestCase):
    """floor/ceil 除法：四种符号组合 + 大整数，以 Fraction 为参照。"""

    def test_small_signs(self):
        cases = [(3, 2), (-3, 2), (3, -2), (-3, -2), (4, 2), (-4, 2),
                 (4, -2), (-4, -2), (0, 5), (0, -5), (1, 3), (-1, 3)]
        for n, d in cases:
            ref = Fraction(n, d)
            self.assertEqual(floor_div(n, d), floor(ref), (n, d))
            self.assertEqual(ceil_div(n, d), ceil(ref), (n, d))

    def test_big_ints(self):
        rng = random.Random(7)
        for _ in range(300):
            n = rng.randrange(-2**260, 2**260)
            d = rng.randrange(1, 2**130) * rng.choice([-1, 1])
            ref = Fraction(n, d)
            self.assertEqual(floor_div(n, d), floor(ref))
            self.assertEqual(ceil_div(n, d), ceil(ref))


class TestBasicPropagation(unittest.TestCase):
    def test_positive_coeff(self):
        # 2x <= 3  =>  x <= floor(3/2) = 1
        s = Solver()
        s.add_variable("x", -10, 10)
        s.add_constraint([(2, "x")], 3)
        self.assertEqual(s.hi["x"], 1)
        self.assertEqual(s.lo["x"], -10)

    def test_negative_coeff(self):
        # -2x <= 3  =>  x >= ceil(3/-2) = -1
        s = Solver()
        s.add_variable("x", -10, 10)
        s.add_constraint([(-2, "x")], 3)
        self.assertEqual(s.lo["x"], -1)
        self.assertEqual(s.hi["x"], 10)

    def test_negative_coeff_negative_rhs(self):
        # -2x <= -5  =>  x >= ceil(-5/-2) = 3
        s = Solver()
        s.add_variable("x", -10, 10)
        s.add_constraint([(-2, "x")], -5)
        self.assertEqual(s.lo["x"], 3)

    def test_positive_coeff_negative_rhs(self):
        # 2x <= -3  =>  x <= floor(-3/2) = -2
        s = Solver()
        s.add_variable("x", -10, 10)
        s.add_constraint([(2, "x")], -3)
        self.assertEqual(s.hi["x"], -2)

    def test_mixed_rounding(self):
        # -3x <= 7  =>  x >= ceil(7/-3) = -2
        s = Solver()
        s.add_variable("x", -10, 10)
        s.add_constraint([(-3, "x")], 7)
        self.assertEqual(s.lo["x"], -2)
        # 3x <= -7  =>  x <= floor(-7/3) = -3
        s2 = Solver()
        s2.add_variable("x", -10, 10)
        s2.add_constraint([(3, "x")], -7)
        self.assertEqual(s2.hi["x"], -3)

    def test_zero_coeff_no_effect(self):
        s = Solver()
        s.add_variable("x", -5, 5)
        s.add_variable("y", -5, 5)
        s.add_constraint([(0, "x"), (1, "y")], 3)
        self.assertEqual((s.lo["x"], s.hi["x"]), (-5, 5))
        self.assertEqual(s.hi["y"], 3)

    def test_constant_contradiction_fails_immediately(self):
        s = Solver()
        s.add_variable("x", 0, 5)
        s.add_constraint([], -1)  # 0 <= -1
        self.assertIsNotNone(s.conflict)
        self.assertEqual(s.solve(), "INFEASIBLE")

    def test_constant_tautology_is_noop(self):
        s = Solver()
        s.add_variable("x", 0, 5)
        s.add_constraint([], 3)  # 0 <= 3
        self.assertIsNone(s.conflict)
        self.assertEqual((s.lo["x"], s.hi["x"]), (0, 5))


class TestEqualitySplit(unittest.TestCase):
    def test_equality_both_directions(self):
        # x + y == 5, y in [2,4]  =>  x in [1,3]
        s = Solver()
        s.add_variable("x", 0, 10)
        s.add_variable("y", 2, 4)
        s.add_constraint([(1, "x"), (1, "y")], 5, sense="==")
        self.assertEqual((s.lo["x"], s.hi["x"]), (1, 3))
        self.assertEqual((s.lo["y"], s.hi["y"]), (2, 4))

    def test_equality_forces_singleton(self):
        s = Solver()
        s.add_variable("x", -10, 10)
        s.add_constraint([(3, "x")], 9, sense="==")
        self.assertEqual((s.lo["x"], s.hi["x"]), (3, 3))
        self.assertEqual(s.solve(), "SATISFIABLE")


class TestQueueFixpoint(unittest.TestCase):
    def test_chained_propagation(self):
        # x <= y, y <= z, z <= 3  =>  x <= 3（经队列逐层传播）
        s = Solver()
        for v in "xyz":
            s.add_variable(v, 0, 10)
        s.add_constraint([(1, "x"), (-1, "y")], 0)
        s.add_constraint([(1, "y"), (-1, "z")], 0)
        s.add_constraint([(1, "z")], 3)
        self.assertEqual(s.hi["z"], 3)
        self.assertEqual(s.hi["y"], 3)
        self.assertEqual(s.hi["x"], 3)

    def test_explanations_recorded(self):
        s = Solver()
        s.add_variable("x", 0, 10)
        s.add_constraint([(2, "x")], 3)
        src, msg = s.why_hi["x"]
        self.assertEqual(src, 0)
        self.assertIn("x <= 1", msg)
        self.assertIn("constraint 0", msg)


class TestUndo(unittest.TestCase):
    def test_domain_restored_after_retract(self):
        s = Solver()
        s.add_variable("x", 0, 10)
        s.add_variable("y", 0, 10)
        s.add_constraint([(1, "x"), (1, "y")], 8)
        self.assertEqual(s.hi["x"], 8)
        mark = s.checkpoint()
        s.add_constraint([(1, "x")], 3)
        self.assertEqual(s.hi["x"], 3)
        s.undo(mark)
        self.assertEqual(s.hi["x"], 8)  # 恢复到撤回前由共享约束推出的界
        self.assertEqual(len(s.constraints), 1)
        self.assertEqual(len(s.original), 1)

    def test_conflict_cleared_and_reusable(self):
        s = Solver()
        s.add_variable("x", 0, 10)
        s.add_constraint([(1, "x")], 6)
        mark = s.checkpoint()
        s.add_constraint([(1, "x")], 12, sense=">=")  # x >= 12 与 x <= 6 矛盾
        self.assertEqual(s.solve(), "INFEASIBLE")
        s.undo(mark)
        self.assertIsNone(s.conflict)
        self.assertEqual((s.lo["x"], s.hi["x"]), (0, 6))
        s.add_constraint([(1, "x")], 4, sense=">=")
        self.assertEqual((s.lo["x"], s.hi["x"]), (4, 6))

    def test_explanation_restored(self):
        s = Solver()
        s.add_variable("x", 0, 10)
        s.add_constraint([(1, "x")], 7)
        why_before = s.why_hi["x"]
        mark = s.checkpoint()
        s.add_constraint([(1, "x")], 2)
        self.assertNotEqual(s.why_hi["x"], why_before)
        s.undo(mark)
        self.assertEqual(s.why_hi["x"], why_before)


class TestCongruence(unittest.TestCase):
    def test_gcd_detects_infeasible_equality(self):
        # 2x + 2y + 2z == 3：lhs 恒为偶数，纯区间传播无法发现
        s = Solver()
        for v in "xyz":
            s.add_variable(v, 0, 5)
        s.add_constraint([(2, "x"), (2, "y"), (2, "z")], 3, sense="==")
        self.assertEqual(s.solve(), "INFEASIBLE")
        self.assertIn("congruence", s.conflict)

    def test_interval_alone_misses_it(self):
        # 关闭同余检查后，同一实例传播至固定点仍域非空 -> UNKNOWN
        s = Solver(use_congruence=False)
        for v in "xyz":
            s.add_variable(v, 0, 5)
        s.add_constraint([(2, "x"), (2, "y"), (2, "z")], 3, sense="==")
        self.assertEqual(s.solve(), "UNKNOWN")
        for v in "xyz":
            self.assertLessEqual(s.lo[v], s.hi[v])

    def test_congruence_tightens_inequality(self):
        # 2x <= 1 且 x >= 1：同余把 rhs 收紧到 0，min lhs = 2 > 0
        s = Solver()
        s.add_variable("x", 1, 5)
        s.add_constraint([(2, "x")], 1)
        self.assertEqual(s.solve(), "INFEASIBLE")

    def test_not_complete_by_design(self):
        # 传播到固定点但域非单点：只能报 UNKNOWN，不得声称已求解
        s = Solver()
        s.add_variable("x", 0, 10)
        s.add_variable("y", 0, 10)
        s.add_constraint([(1, "x"), (1, "y")], 10)
        self.assertEqual(s.solve(), "UNKNOWN")


class TestBigInts(unittest.TestCase):
    def test_huge_coefficients(self):
        big = 2**128
        s = Solver()
        s.add_variable("x", 0, 2**210)
        s.add_constraint([(big, "x")], 3 * big + 1)
        self.assertEqual(s.hi["x"], 3)

    def test_huge_negative_coefficient(self):
        big = 2**127
        s = Solver()
        s.add_variable("x", -(2**210), 2**210)
        # -(2^127) x <= 5*2^127 + 7  =>  x >= ceil(-(5*2^127+7)/2^127) = -5
        s.add_constraint([(-big, "x")], 5 * big + 7)
        self.assertEqual(s.lo["x"], -5)

    def test_huge_equality_singleton(self):
        big = 2**64
        s = Solver()
        s.add_variable("x", -(2**100), 2**100)
        s.add_constraint([(big, "x")], 3 * big, sense="==")
        self.assertEqual((s.lo["x"], s.hi["x"]), (3, 3))
        self.assertEqual(s.solve(), "SATISFIABLE")
        self.assertTrue(check_assignment(s.original, s.assignment()))


class TestSatClaimVerified(unittest.TestCase):
    def test_unique_solution_verified_by_checker(self):
        # 2x == 6, x + y == 5  =>  x = 3, y = 2（界传播可达单点）
        s = Solver()
        s.add_variable("x", 0, 10)
        s.add_variable("y", 0, 10)
        s.add_constraint([(2, "x")], 6, sense="==")
        s.add_constraint([(1, "x"), (1, "y")], 5, sense="==")
        self.assertEqual(s.solve(), "SATISFIABLE")
        assn = s.assignment()
        self.assertEqual(assn, {"x": 3, "y": 2})
        self.assertTrue(check_assignment(s.original, assn))
        sols = enumerate_solutions(s.original, {"x": (0, 10), "y": (0, 10)})
        self.assertEqual(sols, [assn])


class TestReferenceEnumeration(unittest.TestCase):
    """随机小实例：用精确整数枚举逐值检验传播器的每次剪枝。"""

    def test_prunings_are_sound(self):
        rng = random.Random(20241001)
        for trial in range(300):
            nvars = rng.randint(1, 3)
            names = [f"v{i}" for i in range(nvars)]
            domains = {v: (rng.randint(-4, 0), rng.randint(0, 4)) for v in names}
            specs = []
            for _ in range(rng.randint(1, 4)):
                terms = [(rng.randint(-3, 3), v) for v in names
                         if rng.random() < 0.8]
                rhs = rng.randint(-8, 8)
                sense = rng.choice(["<=", "<=", ">=", "=="])
                specs.append((terms, rhs, sense))

            s = Solver()
            for v in names:
                s.add_variable(v, *domains[v])
            for terms, rhs, sense in specs:
                s.add_constraint(terms, rhs, sense=sense)
            status = s.solve()

            original = [(tuple((a, v) for a, v in t), r, se)
                        for t, r, se in specs]
            sols = enumerate_solutions(original, domains)

            if status == "INFEASIBLE":
                self.assertEqual(sols, [], f"trial {trial}: {specs}")
                continue

            # 每个被剪掉的值都不得出现在任何参考解中
            for v in names:
                lo0, hi0 = domains[v]
                pruned = [val for val in range(lo0, hi0 + 1)
                          if not (s.lo[v] <= val <= s.hi[v])]
                for val in pruned:
                    self.assertTrue(
                        all(sol[v] != val for sol in sols),
                        f"trial {trial}: {v}={val} wrongly pruned; {specs}",
                    )
                # 域必须仍然覆盖所有参考解在该变量上的投影
                for sol in sols:
                    self.assertTrue(s.lo[v] <= sol[v] <= s.hi[v])

            if status == "SATISFIABLE":
                assn = s.assignment()
                self.assertTrue(check_assignment(original, assn))
                self.assertIn(assn, sols)

    def test_negative_coeff_rounding_against_enumeration(self):
        # 专挑负系数边界：-a x <= b 的 lo 收紧逐值与枚举投影比对
        rng = random.Random(99)
        for _ in range(200):
            a = rng.randint(1, 5)
            b = rng.randint(-9, 9)
            lo0, hi0 = -6, 6
            s = Solver()
            s.add_variable("x", lo0, hi0)
            s.add_constraint([(-a, "x")], b)
            sols = enumerate_solutions(
                [(((-a, "x"),), b, "<=")], {"x": (lo0, hi0)})
            if s.conflict is not None:
                self.assertEqual(sols, [])  # 冲突必须与枚举一致
                continue
            proj = sorted({sol["x"] for sol in sols})
            kept = list(range(s.lo["x"], s.hi["x"] + 1))
            # 单约束单变量时界传播是精确的：保留区间应等于投影的凸包
            self.assertEqual(kept[0], proj[0])
            self.assertEqual(kept[-1], proj[-1])


if __name__ == "__main__":
    unittest.main()
