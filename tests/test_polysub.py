import unittest
from fractions import Fraction

from polysub import (
    AddExpr,
    BudgetExceeded,
    Expr,
    MulExpr,
    Poly,
    PolyExpr,
    PowExpr,
    _capped_comb,
)

x = Poly.var("x")
y = Poly.var("y")
z = Poly.var("z")
a = Poly.var("a")
b = Poly.var("b")
c = Poly.var("c")


class TestSimultaneousSubstitution(unittest.TestCase):
    def test_swap_variables(self):
        # x→y、y→x 必须是交换，而不是全部变成 x
        p = x + 2 * y
        result = p.substitute({"x": y, "y": x})
        self.assertEqual(result, y + 2 * x)

    def test_swap_with_powers(self):
        p = x**2 + x * y + 1
        result = p.substitute({"x": y, "y": x})
        self.assertEqual(result, y**2 + y * x + 1)

    def test_no_sequential_pollution(self):
        # 顺序代换 x→y 再 y→x 会把所有项污染成 x；同时代换不会
        p = x - y
        simultaneous = p.substitute({"x": y, "y": x})
        self.assertEqual(simultaneous, y - x)
        # 对照：逐步顺序代换确实产生错误结果（演示污染长什么样）
        sequential = p.substitute({"x": y}).substitute({"y": x})
        self.assertEqual(sequential, Poly.zero())
        self.assertNotEqual(simultaneous, sequential)

    def test_replacements_not_resubstituted(self):
        # 替换式里的 y 也在 mapping 中，但不会被二次代换成 1
        p = x + 0 * y  # 变量集为 (x, y)，但只含 x 项
        result = p.substitute({"x": y, "y": 1})
        self.assertEqual(result, y)

    def test_mutual_shift(self):
        # {x: y+1, y: x+1}：x --y → (y+1)-(x+1) = y - x
        p = x - y
        result = p.substitute({"x": y + 1, "y": x + 1})
        self.assertEqual(result, y - x)

    def test_zero_substitution_eliminates_terms(self):
        p = x**2 + x * y + y
        result = p.substitute({"x": 0})
        self.assertEqual(result, y)

    def test_zero_substitution_keeps_constant(self):
        p = x + 2
        self.assertEqual(p.substitute({"x": 0}), Poly.constant(2))

    def test_zero_substitution_kills_everything(self):
        p = x * y + x
        result = p.substitute({"x": 0, "y": 0})
        self.assertTrue(result.is_zero())

    def test_partial_mapping_keeps_other_variables(self):
        p = x + y
        result = p.substitute({"x": 3})
        self.assertEqual(result, 3 + y)

    def test_unknown_variable_rejected(self):
        with self.assertRaises(ValueError):
            (x + 1).substitute({"w": 1})

    def test_float_rejected(self):
        with self.assertRaises(TypeError):
            (x + 1).substitute({"x": 0.5})


class TestExactRational(unittest.TestCase):
    def test_fraction_coefficients_exact(self):
        p = Fraction(1, 3) * x + Fraction(1, 6) * x
        self.assertEqual(p, Fraction(1, 2) * x)
        coeff = next(iter(p.terms.values()))
        self.assertIsInstance(coeff, Fraction)

    def test_pow_exact(self):
        p = (x + Fraction(1, 2)) ** 2
        expected = x**2 + x + Fraction(1, 4)
        self.assertEqual(p, expected)

    def test_substitution_exact(self):
        # x → 1/3 代入 3x^2：精确得 1/3，而非 0.333...
        p = 3 * x**2
        result = p.substitute({"x": Fraction(1, 3)})
        self.assertEqual(result, Poly.constant(Fraction(1, 3)))

    def test_binomial_coefficients_exact(self):
        p = (x + 1) ** 5
        expected = x**5 + 5 * x**4 + 10 * x**3 + 10 * x**2 + 5 * x + 1
        self.assertEqual(p, expected)


class TestBudget(unittest.TestCase):
    def test_within_budget_expands(self):
        p = x**5
        result = p.substitute({"x": a + b}, budget=6)
        self.assertIsInstance(result, Poly)
        self.assertEqual(result, (a + b) ** 5)
        self.assertEqual(result.num_terms, 6)

    def test_over_budget_keeps_unexpanded(self):
        p = x**5
        result = p.substitute({"x": a + b}, budget=5)
        self.assertIsInstance(result, Expr)
        self.assertNotIsInstance(result, Poly)
        self.assertIn("^5", str(result))  # 保留未展开形式

    def test_unexpanded_expands_to_same_value_later(self):
        p = x**5
        deferred = p.substitute({"x": a + b}, budget=5)
        direct = p.substitute({"x": a + b})
        self.assertEqual(deferred.expand(), direct)

    def test_expand_over_budget_raises_not_truncates(self):
        p = x**5
        deferred = p.substitute({"x": a + b}, budget=5)
        with self.assertRaises(BudgetExceeded) as ctx:
            deferred.expand(budget=5)
        # 异常携带未展开表达式，可用更大预算重试
        self.assertIs(ctx.exception.expr, deferred)
        self.assertEqual(ctx.exception.expr.expand(), (a + b) ** 5)

    def test_no_truncated_false_equality(self):
        # 预算中断的结果绝不能是“部分展开的多项式”
        p = x**4 + x**2
        result = p.substitute({"x": a + b}, budget=3)
        self.assertIsInstance(result, Expr)
        # 它与真值相等（经展开后），而不是某个截断 Poly
        self.assertEqual(result.expand(), (a + b) ** 4 + (a + b) ** 2)

    def test_estimate_is_upper_bound(self):
        cases = [
            (a + b, 3),
            (a + b + c, 4),
            (a * b + c, 5),
            (a + 1, 10),
        ]
        for base, n in cases:
            est = PowExpr(PolyExpr(base), n).term_estimate()
            actual = (base**n).num_terms
            self.assertGreaterEqual(est, actual, f"{base}^{n}")

    def test_estimate_merge_case(self):
        # (x+1)*(x+1) 估计 4 项，实际合并为 3 项：估计仍是上界
        est = MulExpr(PolyExpr(x + 1), PolyExpr(x + 1)).term_estimate()
        self.assertEqual(est, 4)
        self.assertEqual(((x + 1) * (x + 1)).num_terms, 3)

    def test_zero_estimate(self):
        self.assertEqual(PowExpr(PolyExpr(Poly.zero()), 3).term_estimate(), 0)
        self.assertEqual(PowExpr(PolyExpr(Poly.zero()), 0).term_estimate(), 1)

    def test_capped_comb(self):
        self.assertEqual(_capped_comb(5, 2), 10)
        self.assertEqual(_capped_comb(5, 9), 0)
        self.assertEqual(_capped_comb(10**9, 10**9 - 1, cap=100), 100)

    def test_budget_on_zero_polynomial(self):
        result = Poly.zero(("x",)).substitute({"x": a + b}, budget=1)
        self.assertTrue(result.is_zero())


class TestPolyArithmetic(unittest.TestCase):
    def test_add_aligns_variables(self):
        p = x + y
        self.assertEqual(p.variables, ("x", "y"))
        self.assertEqual(p, y + x)

    def test_mul_distributes(self):
        p = (x + 1) * (y + 2)
        self.assertEqual(p, x * y + 2 * x + y + 2)

    def test_scalar_ops(self):
        self.assertEqual(2 * x + 1, x + x + 1)
        self.assertEqual(3 - x, -(x - 3))

    def test_pow_rejects_negative_and_bool(self):
        with self.assertRaises(ValueError):
            x**-1
        with self.assertRaises(ValueError):
            x ** True

    def test_zero_pow_zero_is_one(self):
        self.assertEqual(Poly.zero() ** 0, Poly.constant(1))

    def test_equality_across_variable_sets(self):
        self.assertEqual(x + 1, Poly(("x", "y"), {(1, 0): 1, (0, 0): 1}))
        self.assertNotEqual(x, y)

    def test_str_format(self):
        self.assertEqual(str(x**2 * y + 2 * x - 3), "x^2*y + 2*x - 3")
        self.assertEqual(str(Poly.zero()), "0")


if __name__ == "__main__":
    unittest.main()
