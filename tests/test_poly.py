import unittest
from fractions import Fraction

from rational_id.poly import (BudgetExceeded, Poly, TermBudget, add,
                              div_exact, gcd, monic, mul, poly_pow,
                              same_up_to_scalar, squarefree_part, sub)


def P(nvars, d):
    return Poly(nvars, {m: Fraction(c) for m, c in d.items()})


class PolyOpsTest(unittest.TestCase):
    def test_add_mul_expand(self):
        x = Poly.variable(1, 0)
        one = Poly.constant(1, 1)
        p = mul(add(x, one), add(x, one))  # (x+1)^2
        self.assertEqual(p.terms,
                         {(2,): Fraction(1), (1,): Fraction(2), (0,): Fraction(1)})

    def test_fraction_coefficients(self):
        p = P(1, {(1,): Fraction(1, 2), (0,): Fraction(1, 3)})
        q = add(p, p)
        self.assertEqual(q.terms, {(1,): Fraction(1), (0,): Fraction(2, 3)})

    def test_evaluate(self):
        p = P(2, {(2, 0): 1, (0, 2): -1})  # x^2 - y^2
        self.assertEqual(p.evaluate((Fraction(3), Fraction(2))), Fraction(5))

    def test_pow(self):
        x = Poly.variable(1, 0)
        one = Poly.constant(1, 1)
        p = poly_pow(add(x, one), 3)  # (x+1)^3
        self.assertEqual(p.terms, {(3,): 1, (2,): 3, (1,): 3, (0,): 1})

    def test_div_exact(self):
        x = Poly.variable(1, 0)
        one = Poly.constant(1, 1)
        num = mul(add(x, one), sub(x, one))  # x^2 - 1
        q = div_exact(num, sub(x, one))
        self.assertEqual(q.terms, add(x, one).terms)
        with self.assertRaises(ValueError):
            div_exact(add(x, one), P(1, {(1,): 2}))  # (x+1)/(2x) 不整除

    def test_gcd_univariate(self):
        x = Poly.variable(1, 0)
        one = Poly.constant(1, 1)
        a = mul(sub(x, one), add(x, one))  # x^2 - 1
        b = mul(sub(x, one), sub(x, one))  # (x-1)^2
        self.assertEqual(gcd(a, b).terms, sub(x, one).terms)

    def test_gcd_multivariate(self):
        x = Poly.variable(2, 0)
        y = Poly.variable(2, 1)
        a = mul(add(x, y), sub(x, y))  # x^2 - y^2
        b = mul(add(x, y), add(x, y))  # (x+y)^2
        self.assertEqual(gcd(a, b).terms, add(x, y).terms)

    def test_gcd_with_content(self):
        x = Poly.variable(2, 0)
        y = Poly.variable(2, 1)
        a = P(2, {(1, 0): 2, (0, 1): 2})   # 2x + 2y
        b = P(2, {(1, 0): 4, (0, 1): 4})   # 4x + 4y
        self.assertEqual(gcd(a, b).terms, add(x, y).terms)

    def test_gcd_multivariate_mixed(self):
        x = Poly.variable(2, 0)
        y = Poly.variable(2, 1)
        one = Poly.constant(2, 1)
        # (x+y)(x+1) 与 (x+y)(y+1) 的 gcd 为 x+y
        a = mul(add(x, y), add(x, one))
        b = mul(add(x, y), add(y, one))
        self.assertEqual(gcd(a, b).terms, add(x, y).terms)

    def test_squarefree_part(self):
        x = Poly.variable(2, 0)
        y = Poly.variable(2, 1)
        f = mul(mul(add(x, y), add(x, y)), sub(x, y))  # (x+y)^2 (x-y)
        expect = mul(add(x, y), sub(x, y))             # x^2 - y^2
        self.assertEqual(squarefree_part(f).terms, monic(expect).terms)

    def test_squarefree_univariate(self):
        x = Poly.variable(1, 0)
        f = poly_pow(x, 3)  # x^3
        self.assertEqual(squarefree_part(f).terms, x.terms)

    def test_same_up_to_scalar(self):
        a = P(1, {(1,): 2, (0,): 2})
        b = P(1, {(1,): -1, (0,): -1})
        c = P(1, {(1,): 1, (0,): 2})
        self.assertTrue(same_up_to_scalar(a, b))
        self.assertFalse(same_up_to_scalar(a, c))

    def test_budget_exceeded(self):
        x = Poly.variable(1, 0)
        one = Poly.constant(1, 1)
        with self.assertRaises(BudgetExceeded):
            poly_pow(add(x, one), 10, TermBudget(5))  # 11 项 > 5


if __name__ == "__main__":
    unittest.main()
