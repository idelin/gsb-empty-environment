import unittest
from fractions import Fraction

from diffdag import Factory, Differentiator, Simplifier, domain_of, to_str


def make():
    fac = Factory()
    return fac, fac.var("x")


def derive(fac, f_expr):
    differ = Differentiator(fac, "x")
    df = differ.diff(f_expr)
    simp = Simplifier(fac)
    return simp.simplify(df), differ, simp


class TestBasicRules(unittest.TestCase):
    def test_const_rule(self):
        fac, _ = make()
        df, differ, _ = derive(fac, fac.const(Fraction(3, 4)))
        self.assertEqual(to_str(df), "0")
        self.assertEqual(differ.steps[0].rule, "常数法则")

    def test_var_rule(self):
        fac, x = make()
        df, differ, _ = derive(fac, x)
        self.assertEqual(to_str(df), "1")
        self.assertEqual(differ.steps[0].rule, "变量法则")

    def test_other_var_is_const(self):
        fac, _ = make()
        df, _, _ = derive(fac, fac.var("y"))
        self.assertEqual(to_str(df), "0")

    def test_sum_and_power_rule(self):
        fac, x = make()
        f = fac.add(fac.add(fac.pow(x, 3), fac.mul(fac.const(2), x)), fac.const(1))
        df, _, _ = derive(fac, f)
        self.assertEqual(to_str(df), "3*x^2 + 2")

    def test_product_rule(self):
        fac, x = make()
        df, differ, _ = derive(fac, fac.mul(x, fac.sin(x)))
        self.assertEqual(to_str(df), "sin(x) + x*cos(x)")
        rules = {s.rule for s in differ.steps}
        self.assertIn("乘积法则", rules)
        self.assertIn("链式法则·正弦", rules)

    def test_chain_rule_log_of_composite(self):
        fac, x = make()
        u = fac.add(fac.pow(x, 2), fac.const(1))
        df, _, _ = derive(fac, fac.log(u))
        self.assertEqual(to_str(df), "2*x*(x^2 + 1)^(-1)")


class TestExactRational(unittest.TestCase):
    def test_rational_coefficient_exact(self):
        fac, x = make()
        f = fac.mul(fac.const(Fraction(1, 3)), fac.pow(x, 3))
        df, _, _ = derive(fac, f)
        self.assertEqual(to_str(df), "x^2")

    def test_mixed_rational_polynomial(self):
        fac, x = make()
        f = fac.add(fac.mul(fac.const(Fraction(3, 2)), fac.pow(x, 2)), x)
        df, _, _ = derive(fac, f)
        self.assertEqual(to_str(df), "3*x + 1")

    def test_const_folding_exact(self):
        fac, _ = make()
        simp = Simplifier(fac)
        out = simp.simplify(fac.add(fac.const(Fraction(1, 3)), fac.const(Fraction(1, 6))))
        self.assertEqual(out.value, Fraction(1, 2))

    def test_half_power_coefficient(self):
        fac, x = make()
        df, _, _ = derive(fac, fac.pow(x, Fraction(1, 2)))
        self.assertEqual(to_str(df), "(1/2)*x^(-1/2)")


class TestDomain(unittest.TestCase):
    def test_log_domain_preserved(self):
        fac, x = make()
        f = fac.log(x)
        df, differ, _ = derive(fac, f)
        self.assertEqual(to_str(df), "x^(-1)")
        self.assertIn("x > 0", domain_of(f))
        log_steps = [s for s in differ.steps if s.rule == "链式法则·对数"]
        self.assertTrue(any("x > 0" in s.domain for s in log_steps))

    def test_negative_exponent_domain(self):
        fac, x = make()
        f = fac.pow(x, -2)
        df, differ, _ = derive(fac, f)
        self.assertEqual(to_str(df), "(-2)*x^(-3)")
        self.assertIn("x ≠ 0", domain_of(f))
        pow_steps = [s for s in differ.steps if s.rule == "幂法则"]
        self.assertTrue(any("≠ 0" in s.domain for s in pow_steps))

    def test_noninteger_exponent_domain(self):
        fac, x = make()
        f = fac.pow(x, Fraction(1, 2))
        self.assertIn("x > 0", domain_of(f))

    def test_simplify_does_not_change_domain(self):
        # 0·log(x) 化简为 0，但管线保留原定义域 {x > 0}
        fac, x = make()
        f = fac.mul(fac.const(0), fac.log(x))
        dom_before = domain_of(f)
        simp = Simplifier(fac)
        out = simp.simplify(f)
        self.assertEqual(to_str(out), "0")
        self.assertIn("x > 0", dom_before)  # 定义域由管线保留，不随化简丢失
        zero_steps = [s for s in simp.steps if s.rule == "乘法零元"]
        self.assertTrue(any("定义域" in s.domain for s in zero_steps))

    def test_every_step_has_domain(self):
        fac, x = make()
        f = fac.mul(fac.sin(x), fac.log(x))
        _, differ, simp = derive(fac, f)
        for s in differ.steps:
            self.assertTrue(s.domain)
        for s in simp.steps:
            self.assertTrue(s.domain)


class TestZeroExponent(unittest.TestCase):
    def test_zero_power_is_constant_one(self):
        fac, x = make()
        f = fac.pow(x, 0)
        df, differ, _ = derive(fac, f)
        self.assertEqual(to_str(df), "0")
        step = [s for s in differ.steps if s.rule == "零指数约定"]
        self.assertEqual(len(step), 1)
        self.assertIn("u = 0", step[0].detail)  # 特殊点语义明确：0^0 = 1

    def test_zero_power_simplifies_to_one(self):
        fac, x = make()
        simp = Simplifier(fac)
        out = simp.simplify(fac.pow(x, 0))
        self.assertEqual(to_str(out), "1")
        self.assertEqual(simp.steps[0].rule, "零指数约定")

    def test_zero_power_domain_unrestricted(self):
        fac, x = make()
        self.assertEqual(domain_of(fac.pow(x, 0)), ())


class TestDAGSharing(unittest.TestCase):
    def test_hash_consing_shares_nodes(self):
        fac, x = make()
        y1 = fac.add(fac.pow(x, 2), fac.const(1))
        y2 = fac.add(fac.pow(x, 2), fac.const(1))
        self.assertIs(y1, y2)

    def test_shared_subgraph_differentiated_once(self):
        fac, x = make()
        y = fac.add(fac.pow(x, 2), fac.const(1))
        f = fac.mul(fac.mul(y, y), y)  # y 被引用 3 次
        _, differ, _ = derive(fac, f)
        power_steps = [s for s in differ.steps if s.rule == "幂法则"]
        self.assertEqual(len(power_steps), 1)  # x^2 只求导一次
        self.assertGreaterEqual(differ.reuse_count, 2)  # 其余引用命中缓存
        reuse = [s for s in differ.steps if s.rule == "复用"]
        self.assertTrue(any("不重复展开" in s.detail for s in reuse))

    def test_deep_shared_dag_node_count(self):
        fac, x = make()
        y1 = fac.add(fac.pow(x, 2), fac.const(1))
        y2 = fac.add(fac.mul(y1, y1), y1)
        y3 = fac.add(fac.mul(y2, y2), y2)
        # 哈希一致化：构造阶段仅 9 个节点（树展开则有 2^4 级规模）
        self.assertLessEqual(fac.node_count, 9)
        _, differ, _ = derive(fac, y3)
        # 结构共享：y1/y2 的导数各算一次，复用次数 > 0
        self.assertGreater(differ.reuse_count, 0)


class TestSimplify(unittest.TestCase):
    def test_additive_identity(self):
        fac, x = make()
        simp = Simplifier(fac)
        out = simp.simplify(fac.add(fac.const(0), x))
        self.assertIs(out, x)

    def test_multiplicative_identity(self):
        fac, x = make()
        simp = Simplifier(fac)
        out = simp.simplify(fac.mul(fac.const(1), fac.sin(x)))
        self.assertEqual(to_str(out), "sin(x)")

    def test_unit_exponent(self):
        fac, x = make()
        simp = Simplifier(fac)
        out = simp.simplify(fac.pow(fac.sin(x), 1))
        self.assertEqual(to_str(out), "sin(x)")

    def test_combine_like_terms(self):
        fac, x = make()
        simp = Simplifier(fac)
        out = simp.simplify(fac.add(x, x))
        self.assertEqual(to_str(out), "2*x")

    def test_const_power_folding(self):
        fac, _ = make()
        simp = Simplifier(fac)
        out = simp.simplify(fac.pow(fac.const(Fraction(2, 3)), 2))
        self.assertEqual(out.value, Fraction(4, 9))


if __name__ == "__main__":
    unittest.main()
