"""simsub 的单元测试（Python 3.11，仅标准库 unittest）。"""
import random
import unittest
from fractions import Fraction

from simsub import (
    Const, PolynomialError, add, const, eval_poly, expand, format_poly,
    mul, parse, pow_, replace_vars, simultaneous_substitute, sub,
    substitute_string, term_bound, var,
)


def P(text):
    """解析并展开，便于直接比较 {单项式: Fraction}。"""
    return expand(parse(text))


class SimultaneousSubstitutionTest(unittest.TestCase):
    def test_swap_x_y_is_exchange_not_collapse(self):
        # x->y、y->x 必须交换，而不是全部变成 x（或 y）
        r = substitute_string("x + 2*y", {"x": "y", "y": "x"})
        self.assertTrue(r.expanded)
        self.assertEqual(r.poly, P("2*x + y"))

        r = substitute_string("x**2 + y", {"x": "y", "y": "x"})
        self.assertEqual(r.poly, P("y**2 + x"))

    def test_rotation_of_three_variables(self):
        # x->y、y->z、z->x 同时轮转
        r = substitute_string("x + 2*y + 3*z", {"x": "y", "y": "z", "z": "x"})
        self.assertEqual(r.poly, P("y + 2*z + 3*x"))

    def test_sequential_substitution_would_be_contaminated(self):
        # 对照：顺序代换 x->y 再 y->x 会把所有项塌缩成 3*x
        e = parse("x + 2*y")
        sequential = replace_vars(replace_vars(e, {"x": parse("y")}),
                                  {"y": parse("x")})
        self.assertEqual(expand(sequential), P("3*x"))  # 顺序代换的错误结果
        simultaneous = replace_vars(e, {"x": parse("y"), "y": parse("x")})
        self.assertEqual(expand(simultaneous), P("2*x + y"))  # 同时代换正确

    def test_partial_substitution_keeps_other_variables(self):
        r = substitute_string("x*y + z", {"x": "u + 1"})
        self.assertEqual(r.poly, P("u*y + y + z"))

    def test_mapping_may_introduce_new_variables(self):
        r = substitute_string("x**2", {"x": "a + b"})
        self.assertEqual(r.poly, P("a**2 + 2*a*b + b**2"))


class ZeroCancellationTest(unittest.TestCase):
    def test_zero_factor_kills_whole_term(self):
        # x->y 使 (x-y) 变 0，整项 (x-y)*z 消去，而不是留下 0*z
        r = substitute_string("(x - y)*z + x*y", {"x": "y"})
        self.assertEqual(r.poly, P("y**2"))

    def test_everything_cancels_to_zero_polynomial(self):
        r = substitute_string("(x - y)*z", {"x": "y"})
        self.assertTrue(r.expanded)
        self.assertEqual(r.poly, {})  # 零多项式：没有任何项
        self.assertEqual(r.terms, 0)

    def test_substitute_zero_constant(self):
        r = substitute_string("x**2 + x + 1", {"x": "0"})
        self.assertEqual(r.poly, P("1"))

    def test_cancellation_after_substitution(self):
        # 代换后同类项相消：x->y 使 x**2 - y**2 整体归零
        r = substitute_string("x**2 - y**2 + z", {"x": "y"})
        self.assertEqual(r.poly, P("z"))


class ExactRationalTest(unittest.TestCase):
    def test_fraction_coefficients_are_exact(self):
        r = substitute_string("(1/2)*x + 1/3", {"x": "x + 1/7"})
        expected = {(): Fraction(1, 2) * Fraction(1, 7) + Fraction(1, 3),
                    (("x", 1),): Fraction(1, 2)}
        self.assertEqual(r.poly, expected)
        for c in r.poly.values():
            self.assertIsInstance(c, Fraction)

    def test_no_float_anywhere(self):
        r = substitute_string("(2/3)*x**2*y - (1/5)*y**3", {"x": "x + 1/2"})
        for c in r.poly.values():
            self.assertIsInstance(c, Fraction)
        self.assertEqual(r.poly, P("(2/3)*(x + 1/2)**2*y - (1/5)*y**3"))

    def test_parse_rejects_imprecise_or_invalid_input(self):
        with self.assertRaises(PolynomialError):
            parse("0.1*x")          # 浮点不精确，拒绝
        with self.assertRaises(PolynomialError):
            parse("x**(-1)")        # 负指数越出多项式环
        with self.assertRaises(PolynomialError):
            parse("x/y")            # 分母含变量，不是多项式
        with self.assertRaises(PolynomialError):
            parse("x/0")            # 除零
        with self.assertRaises(PolynomialError):
            parse("__import__('os')")  # 白名单之外的语法


class BudgetTest(unittest.TestCase):
    BIG = "(x + y + z)**10"
    MAP = {"x": "a + b"}

    def test_abort_returns_unexpanded_exact_form(self):
        r = substitute_string(self.BIG, self.MAP, budget=100)
        self.assertFalse(r.expanded)
        self.assertTrue(r.stopped_by_budget)
        self.assertIsNone(r.poly)          # 没有部分展开结果
        self.assertIsNone(r.terms)
        self.assertIs(r.result, r.substituted)  # 返回的是未展开 AST
        # 未展开形式仍是精确等式：强制展开后与无预算展开一致
        full = substitute_string(self.BIG, self.MAP, budget=10**9)
        self.assertTrue(full.expanded)
        self.assertEqual(expand(r.result), full.poly)

    def test_abort_is_not_a_truncated_equality(self):
        r = substitute_string(self.BIG, self.MAP, budget=100)
        # 预算中断时绝不把“部分和”伪装成结果：poly 必须为 None
        self.assertIsNone(r.poly)
        # 且未展开形式在任意有理点上的取值 == 完整展开的取值
        rng = random.Random(7)
        full = substitute_string(self.BIG, self.MAP, budget=10**9).poly
        for _ in range(20):
            point = {v: Fraction(rng.randint(-5, 5), rng.randint(1, 6))
                     for v in "abyz"}
            self.assertEqual(eval_poly(expand(r.result), point),
                             eval_poly(full, point))

    def test_budget_boundary_allows_exact_fit(self):
        probe = substitute_string(self.BIG, self.MAP, budget=10**9)
        exact = probe.terms
        # 预算恰好等于真实项数时……保守上界更大，仍会停——这正是“保守”的含义
        r = substitute_string(self.BIG, self.MAP, budget=exact)
        self.assertFalse(r.expanded)
        # 预算达到保守上界时必须展开
        r = substitute_string(self.BIG, self.MAP, budget=r.estimate)
        self.assertTrue(r.expanded)
        self.assertEqual(r.terms, exact)

    def test_negative_budget_rejected(self):
        with self.assertRaises(ValueError):
            substitute_string("x", {"x": "y"}, budget=-1)


class TermBoundSoundnessTest(unittest.TestCase):
    """随机表达式：term_bound 恒不小于真实展开项数。"""

    def _random_expr(self, rng, depth):
        if depth == 0 or rng.random() < 0.3:
            choice = rng.random()
            if choice < 0.4:
                return var(rng.choice("xyz"))
            return const(Fraction(rng.randint(-3, 3), rng.randint(1, 4)))
        op = rng.random()
        if op < 0.4:
            return add(*(self._random_expr(rng, depth - 1)
                         for _ in range(rng.randint(1, 3))))
        if op < 0.8:
            return mul(*(self._random_expr(rng, depth - 1)
                         for _ in range(rng.randint(1, 3))))
        return pow_(self._random_expr(rng, depth - 1), rng.randint(0, 3))

    def test_bound_never_underestimates(self):
        rng = random.Random(20260923)
        for _ in range(300):
            e = self._random_expr(rng, depth=3)
            self.assertGreaterEqual(term_bound(e), len(expand(e)),
                                    f"上界失守: {e!r}")

    def test_substitution_homomorphism_on_random_points(self):
        # 代换后再展开 == 先展开再在映射后的点上求值（环同态性质）
        rng = random.Random(11)
        for _ in range(100):
            e = self._random_expr(rng, depth=3)
            mapping = {v: self._random_expr(rng, 1) for v in "xyz"}
            r = simultaneous_substitute(e, mapping, budget=10**6)
            self.assertTrue(r.expanded)
            point = {v: Fraction(rng.randint(-4, 4), rng.randint(1, 5))
                     for v in "xyz"}
            mapped_point = {v: eval_poly(expand(m), point)
                            for v, m in mapping.items()}
            self.assertEqual(eval_poly(r.poly, point),
                             eval_poly(expand(e), mapped_point))


class StepsNarrationTest(unittest.TestCase):
    def test_every_step_states_its_domain(self):
        r = substitute_string("(x + y)**2", {"x": "y"}, budget=1000)
        self.assertGreaterEqual(len(r.steps), 3)
        for step in r.steps:
            self.assertTrue(step.note.strip())
            self.assertTrue(step.domain.strip())

    def test_aborted_run_mentions_budget_stop(self):
        r = substitute_string("(x + y + z)**10", {"x": "a + b"}, budget=5)
        self.assertIn("停止展开", r.steps[-1].note)


class FormatTest(unittest.TestCase):
    def test_format_poly_is_deterministic(self):
        r1 = substitute_string("x*y + y*z + x*z", {"x": "a + 1"})
        r2 = substitute_string("x*y + y*z + x*z", {"x": "a + 1"})
        self.assertEqual(format_poly(r1.poly), format_poly(r2.poly))
        self.assertEqual(format_poly(P("0")), "0")


if __name__ == "__main__":
    unittest.main()
