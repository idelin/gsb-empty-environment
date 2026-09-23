"""多项式：精确运算、预算、GCD、有理根。"""

import unittest
from fractions import Fraction

from ratid.errors import BudgetExceeded
from ratid.polynomial import (
    Poly,
    eval_poly,
    gcd_poly,
    multiply,
    power,
    rational_roots,
)


class TestPolyArithmetic(unittest.TestCase):
    def test_add_sub(self):
        self.assertEqual(Poly([1, 2, 3]) + Poly([4, 5]), Poly([5, 7, 3]))
        self.assertEqual(Poly([1, 2]) - Poly([1, 2]), Poly([0]))

    def test_multiply_expand(self):
        # (x + 1)(x - 1) = x^2 - 1
        self.assertEqual(multiply(Poly([1, 1]), Poly([-1, 1]), None), Poly([-1, 0, 1]))
        # 幂与二项式系数
        self.assertEqual(power(Poly([1, 1]), 4, None), Poly([1, 4, 6, 4, 1]))
        self.assertEqual(power(Poly([2]), 10, None), Poly.const(1024))

    def test_exact_eval(self):
        p = Poly([-1, 0, 1])
        self.assertEqual(eval_poly(p, Fraction(1, 3)), Fraction(-8, 9))
        self.assertEqual(eval_poly(p, Fraction(1)), Fraction(0))

    def test_budget(self):
        with self.assertRaises(BudgetExceeded):
            power(Poly([1, 1]), 10, budget=4)
        # (1+x+x^3+x^7)^2 的指数和互异，共 10 项，预算 9 必超
        sparse = Poly([1 if k in (0, 1, 3, 7) else 0 for k in range(8)])
        with self.assertRaises(BudgetExceeded):
            multiply(sparse, sparse, budget=9)
    def test_budget_allows_full_cancellation(self):
        # 展开项数超限才报；两项数小但中间抵消的情形必须正常
        big = Poly([1, 1]) + Poly([-1, -1])
        self.assertTrue(big.is_zero)


class TestGcd(unittest.TestCase):
    def test_basic_gcd(self):
        # gcd(x^2-1, x-1) = x-1（差一个符号归一）
        self.assertEqual(gcd_poly(Poly([-1, 0, 1]), Poly([-1, 1])), Poly([-1, 1]))
        self.assertEqual(gcd_poly(Poly([0, 1]), Poly([1, 1])), Poly([1]))

    def test_repeated_factor_gcd(self):
        # gcd((x-1)^2, (x-1)(x+1)) 是 x-1 的相伴
        a = power(Poly([-1, 1]), 2, None)
        b = multiply(Poly([-1, 1]), Poly([1, 1]), None)
        self.assertEqual(gcd_poly(a, b), Poly([-1, 1]))


class TestRationalRoots(unittest.TestCase):
    def test_multiple_distinct_roots(self):
        # x(x+1)
        self.assertEqual(rational_roots(Poly([0, 1, 1])),
                         {Fraction(0): 1, Fraction(-1): 1})

    def test_multiplicities(self):
        # x^2 (x+1)^2
        self.assertEqual(rational_roots(Poly([0, 0, 1, 2, 1])),
                         {Fraction(0): 2, Fraction(-1): 2})

    def test_irrational_factor_left(self):
        # (x-1)(x^2+x+1)：只有有理根 1
        self.assertEqual(rational_roots(Poly([-1, 0, 0, 1])), {Fraction(1): 1})

    def test_fractional_root(self):
        # (2x-1)(3x+2) = 6x^2 + x - 2
        roots = rational_roots(Poly([-2, 1, 6]))
        self.assertEqual(set(roots), {Fraction(1, 2), Fraction(-2, 3)})

    def test_no_roots(self):
        self.assertEqual(rational_roots(Poly([1, 0, 1])), {})  # x^2 + 1

    def test_root_enumeration_budget(self):
        # 人为把候选枚举预算压到极小：x-1 的候选只有 ±1，预算 1 必超
        with self.assertRaises(BudgetExceeded):
            rational_roots(Poly([-1, 1]), budget=1)


if __name__ == "__main__":
    unittest.main()
