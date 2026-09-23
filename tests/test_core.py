"""核心语义测试：合并同类项、排序、加乘、偏导、边界情形。"""

import unittest
from fractions import Fraction

from sparsepoly import Polynomial


def poly(nvars, d):
    return Polynomial(nvars, {tuple(k): Fraction(v) for k, v in d.items()})


class TestBasicSemantics(unittest.TestCase):
    def test_x_minus_x_is_zero_polynomial(self):
        x = Polynomial.variable(0, 2)
        z = x - x
        self.assertTrue(z.is_zero())
        self.assertEqual(len(z.terms), 0)  # 零项回收
        self.assertEqual(z.to_string(), "0")
        self.assertEqual(z, Polynomial.zero(2))

    def test_cross_terms_merge(self):
        # (x + y)^2 = x^2 + 2xy + y^2：交叉项精确合并为单项 2xy
        x = Polynomial.variable(0, 2)
        y = Polynomial.variable(1, 2)
        p = (x + y) * (x + y)
        self.assertEqual(p.terms, {(2, 0): Fraction(1),
                                   (1, 1): Fraction(2),
                                   (0, 2): Fraction(1)})

    def test_addition_combines_and_reclaims(self):
        x = Polynomial.variable(0, 2)
        y = Polynomial.variable(1, 2)
        p = (x + y) + (x - y)  # y 项抵消回收
        self.assertEqual(p.terms, {(1, 0): Fraction(2)})

    def test_rational_coefficients_exact(self):
        p = poly(1, {(1,): Fraction(1, 2)})
        q = poly(1, {(1,): Fraction(1, 3)})
        self.assertEqual((p + q).terms, {(1,): Fraction(5, 6)})
        r = p * q
        self.assertEqual(r.terms, {(2,): Fraction(1, 6)})

    def test_negative_exponent_rejected(self):
        with self.assertRaises(ValueError):
            Polynomial(2, {(-1, 0): Fraction(1)})
        with self.assertRaises(ValueError):
            Polynomial(1, {(-3,): Fraction(2)})

    def test_zero_variable_constant_defined(self):
        c = Polynomial.constant(Fraction(7, 2), nvars=0)
        self.assertEqual(c.terms, {(): Fraction(7, 2)})
        d = Polynomial.constant(2, nvars=0)
        self.assertEqual((c + d).terms, {(): Fraction(11, 2)})
        self.assertEqual((c * d).terms, {(): Fraction(7)})
        self.assertEqual(c.to_string(), "7/2")
        with self.assertRaises(IndexError):
            c.partial(0)

    def test_constant_in_nvars(self):
        c = Polynomial.constant(3, nvars=2)
        self.assertEqual(c.terms, {(0, 0): Fraction(3)})

    def test_partial_derivative(self):
        # d/dx (x^2 y + 3y) = 2xy
        p = poly(2, {(2, 1): 1, (0, 1): 3})
        dp = p.partial(0)
        self.assertEqual(dp.terms, {(1, 1): Fraction(2)})
        # d/dy (x^2 y + 3y) = x^2 + 3
        dq = p.partial(1)
        self.assertEqual(dq.terms, {(2, 0): Fraction(1), (0, 0): Fraction(3)})
        # 常数偏导为零多项式
        self.assertTrue(Polynomial.constant(5, 2).partial(0).is_zero())

    def test_canonical_sorted_output(self):
        p = poly(2, {(0, 2): 1, (1, 0): 1, (2, 0): 1, (1, 1): 2, (0, 0): 5})
        exps = [e for e, _ in p.sorted_terms()]
        self.assertEqual(exps, [(2, 0), (1, 1), (0, 2), (1, 0), (0, 0)])
        self.assertEqual(p.to_string(["x", "y"]), "x^2 + 2*x*y + y^2 + x + 5")

    def test_nvars_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            Polynomial.variable(0, 2) + Polynomial.variable(0, 3)
        with self.assertRaises(ValueError):
            Polynomial.variable(0, 2) * Polynomial.zero(1)

    def test_multiply_by_zero_and_one(self):
        x = Polynomial.variable(0, 1)
        self.assertTrue((x * Polynomial.zero(1)).is_zero())
        self.assertEqual(x * Polynomial.constant(1, 1), x)

    def test_fraction_arithmetic_no_float(self):
        p = poly(1, {(3,): Fraction(1, 3)}) * poly(1, {(2,): Fraction(3, 7)})
        self.assertEqual(p.terms, {(5,): Fraction(1, 7)})


if __name__ == "__main__":
    unittest.main()
