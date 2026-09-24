import unittest
from fractions import Fraction

from differentiator import (
    ZERO, ONE, TWO, NEG_ONE,
    Const, Var, Add, Mul, Pow, Sin, Cos, Tan, Exp, Log,
    DiffEngine, Simplifier, count_nodes, to_str,
)

x = Var("x")
y = Var("y")


def simplified(f, var="x"):
    eng = DiffEngine(var)
    df = eng.derive(f)
    simp = Simplifier()
    return eng, simp.simplify(df), simp


class TestDiffRules(unittest.TestCase):
    def test_const_and_var(self):
        eng = DiffEngine("x")
        self.assertIs(eng.derive(Const(Fraction(3, 7))), ZERO)
        self.assertIs(eng.derive(x), ONE)
        self.assertIs(eng.derive(y), ZERO)
        self.assertEqual(eng.trace[1].rule, "自变量规则 x' = 1")

    def test_sum_and_product(self):
        _, dfs, _ = simplified(Add(Mul(x, x), Const(3)))
        self.assertIs(dfs, Mul(TWO, x))  # (x²+3)' = 2x

    def test_power_rule_exact_fraction(self):
        # (1/3)x³ + (1/2)x² 的导数精确为 x² + x，无浮点误差
        f = Add(Mul(Const(Fraction(1, 3)), Pow(x, Const(3))),
                Mul(Const(Fraction(1, 2)), Pow(x, TWO)))
        _, dfs, _ = simplified(f)
        self.assertIs(dfs, Add(Pow(x, TWO), x))

    def test_chain_sin(self):
        _, dfs, _ = simplified(Sin(Mul(TWO, x)))
        self.assertIs(dfs, Mul(TWO, Cos(Mul(TWO, x))))

    def test_exp_and_tan(self):
        eng, dfs, _ = simplified(Exp(x))
        self.assertIs(dfs, Exp(x))
        eng2 = DiffEngine("x")
        eng2.derive(Tan(x))
        self.assertTrue(any("cos u ≠ 0" in t.domain for t in eng2.trace))

    def test_log_keeps_positive_domain(self):
        eng, dfs, _ = simplified(Log(x))
        self.assertIs(dfs, Pow(x, NEG_ONE))  # (ln x)' = 1/x
        log_steps = [t for t in eng.trace if "ln" in t.rule]
        self.assertTrue(any("u>0" in t.domain for t in log_steps))

    def test_general_power_domain(self):
        eng = DiffEngine("x")
        eng.derive(Pow(x, x))
        self.assertTrue(any("对数求导法" in t.rule and "u>0" in t.domain
                            for t in eng.trace))

    def test_noninteger_and_negative_power_domain(self):
        eng = DiffEngine("x")
        eng.derive(Pow(x, Const(Fraction(1, 2))))
        self.assertTrue(any("u>0" in t.domain and "非整数" in t.domain
                            for t in eng.trace))
        eng2 = DiffEngine("x")
        eng2.derive(Pow(x, Const(-2)))
        self.assertTrue(any("u≠0" in t.domain for t in eng2.trace))


class TestDAGSharing(unittest.TestCase):
    def test_interning(self):
        self.assertIs(Add(x, ONE), Add(x, ONE))
        self.assertIs(Pow(x, TWO), Pow(x, TWO))

    def test_shared_subgraph_derived_once(self):
        g = Add(Pow(x, TWO), ONE)          # 共享子图
        f = Add(Pow(g, Const(3)), Pow(g, TWO))
        eng = DiffEngine("x")
        eng.derive(f)
        # g 被 g³ 与 g² 共同引用，但求导轨迹中 g 只出现一次
        g_hits = [t for t in eng.trace if t.expr is g]
        self.assertEqual(len(g_hits), 1)
        self.assertLess(eng.compute_count, count_nodes(f))
        before = eng.compute_count
        eng.derive(f)                      # 再次求导全部命中缓存
        self.assertEqual(eng.compute_count, before)

    def test_exponent_not_expanded(self):
        f = Pow(Add(x, ONE), Const(50))
        _, dfs, _ = simplified(f)
        self.assertIn("(x + 1)^49", to_str(dfs))
        self.assertLessEqual(count_nodes(dfs), 10)  # 50 次幂没有展开成乘积链


class TestSimplifyDomain(unittest.TestCase):
    def test_zero_factor_requires_entire(self):
        simp = Simplifier()
        self.assertIs(simp.simplify(Mul(ZERO, x)), ZERO)
        simp2 = Simplifier()
        node = Mul(ZERO, Log(x))           # ln x 定义域受限，不能消去
        self.assertIs(simp2.simplify(node), node)

    def test_pow_zero_convention(self):
        simp = Simplifier()
        self.assertIs(simp.simplify(Pow(x, ZERO)), ONE)
        self.assertTrue(any("0^0" in s.domain for s in simp.steps))
        simp2 = Simplifier()
        node = Pow(Log(x), ZERO)           # ln(x)^0 的定义域是 x>0，不能化简为 1
        self.assertIs(simp2.simplify(node), node)

    def test_zero_power_zero(self):
        simp = Simplifier()
        self.assertIs(simp.simplify(Pow(ZERO, ZERO)), ONE)
        self.assertTrue(any("0^0=1" in s.domain for s in simp.steps))

    def test_diff_of_pow_zero_is_zero(self):
        eng = DiffEngine("x")
        self.assertIs(eng.derive(Pow(x, ZERO)), ZERO)
        self.assertTrue(any("0^0" in t.rule for t in eng.trace))

    def test_no_domain_widening(self):
        simp = Simplifier()
        node = Mul(x, Pow(x, NEG_ONE))     # x·x⁻¹ 在 x=0 无定义，不能化简为 1
        self.assertIs(simp.simplify(node), node)

    def test_coefficient_merge(self):
        simp = Simplifier()
        self.assertIs(simp.simplify(Mul(Mul(TWO, x), Const(3))),
                      Mul(Const(6), x))


class TestExactArithmetic(unittest.TestCase):
    def test_fraction_fold(self):
        simp = Simplifier()
        node = Add(Const(Fraction(1, 3)), Const(Fraction(1, 6)))
        self.assertIs(simp.simplify(node), Const(Fraction(1, 2)))

    def test_rational_power_fold(self):
        simp = Simplifier()
        node = Pow(Const(Fraction(2, 3)), Const(-2))
        self.assertIs(simp.simplify(node), Const(Fraction(9, 4)))

    def test_fraction_coefficients_exact(self):
        f = Pow(x, Const(Fraction(5, 2)))
        eng = DiffEngine("x")
        df = eng.derive(f)
        # 导数系数精确为 5/2
        coef = df.args[0].args[0]
        self.assertEqual(coef.value, Fraction(5, 2))


if __name__ == "__main__":
    unittest.main(verbosity=2)
