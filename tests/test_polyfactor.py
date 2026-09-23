import unittest
from fractions import Fraction

from polyfactor import Dag, Poly, factor_polynomial, parse


class TestPolyBasics(unittest.TestCase):
    def test_parse_roundtrip(self):
        for text in ["6*x^2*y + 9*x*y^2", "1/2*x - 3/4*y^2", "x", "0", "5"]:
            poly = parse(text)
            self.assertEqual(parse(str(poly)), poly)

    def test_canonical_equality(self):
        a = parse("x + y")
        b = parse("y + x")
        self.assertEqual(a, b)
        self.assertEqual(a.terms, b.terms)

    def test_zero_coefficients_dropped(self):
        poly = parse("x - x")
        self.assertTrue(poly.is_zero())
        self.assertEqual(str(poly), "0")

    def test_negative_exponent_rejected(self):
        with self.assertRaises(ValueError):
            parse("x^(-1)")

    def test_exact_rational_arithmetic(self):
        poly = parse("1/3*x + 1/6*x")
        self.assertEqual(poly.terms[(1,)], Fraction(1, 2))


class TestContent(unittest.TestCase):
    def test_integer_content(self):
        poly = parse("6*x^2*y + 9*x*y^2")
        c, m = poly.content()
        self.assertEqual(c, Fraction(3))
        self.assertEqual(m, (1, 1))
        self.assertEqual(str(poly.divide_by_content(c, m)), "3*y + 2*x")  # 规范序: 单项式元组升序

    def test_rational_gcd(self):
        poly = parse("1/2*x + 1/3*y")
        c, m = poly.content()
        self.assertEqual(c, Fraction(1, 6))
        self.assertEqual(m, (0, 0))
        q = poly.divide_by_content(c, m)
        self.assertEqual(str(q), "2*y + 3*x")  # 规范序: 单项式元组升序

    def test_multiply_back_strict_equality(self):
        for text in ["6*x^2*y + 9*x*y^2", "2/3*a*b - 4/5*a^2", "7*x"]:
            poly = parse(text)
            c, m = poly.content()
            cm = Poly.monomial(poly.variables, c, m)
            self.assertEqual(cm * poly.divide_by_content(c, m), poly)

    def test_zero_polynomial_has_no_content(self):
        with self.assertRaises(ValueError):
            Poly.zero(("x",)).content()


class TestDag(unittest.TestCase):
    def test_shared_subexpression_counted_once(self):
        dag = Dag()
        x, y, z = dag.var("x"), dag.var("y"), dag.var("z")
        shared = dag.add(x, y)              # (x + y)
        m1 = dag.mul(x, shared)             # x*(x + y)
        m2 = dag.mul(z, shared)             # z*(x + y)
        root = dag.add(m1, m2)
        # nodes: add(x,y), mul(x,add), mul(z,add), add(mul,mul) -> 4, not 5
        self.assertEqual(dag.cost(root), 4)

    def test_expansion_matches_poly_arithmetic(self):
        dag = Dag()
        x, y = dag.var("x"), dag.var("y")
        root = dag.mul(dag.const(3), dag.mul(x, dag.add(x, y)))
        self.assertEqual(dag.expand(root, ("x", "y")), parse("3*x^2 + 3*x*y"))


class TestSearch(unittest.TestCase):
    def test_classic_example_verified(self):
        result = factor_polynomial("6*x^2*y + 9*x*y^2")
        self.assertTrue(result.verified)
        self.assertTrue(result.exact)
        # 展开式 3 次运算; 提取 3xy 需 5 次, 优化器诚实选择更便宜者
        self.assertEqual(result.cost, 3)
        self.assertEqual(result.lower, 3)

    def test_grouping_beats_expanded(self):
        result = factor_polynomial("x^2 + x*y + x*z + y*z")
        self.assertTrue(result.verified)
        self.assertTrue(result.exact)
        self.assertEqual(result.cost, 4)  # x*(x+y) + z*(x+y), 共享 (x+y)
        self.assertLess(result.cost, 5)   # 展开式需要 5 次运算

    def test_zero_polynomial_special_case(self):
        result = factor_polynomial("0")
        self.assertTrue(result.verified)
        self.assertTrue(result.exact)
        self.assertEqual(result.cost, 0)
        self.assertEqual(result.steps[0].rule, "zero-polynomial")

    def test_every_step_verified_and_domain_recorded(self):
        result = factor_polynomial("x^2 + x*y + x*z + y*z")
        self.assertGreater(len(result.steps), 1)
        for step in result.steps:
            self.assertTrue(step.verified, step)
            self.assertTrue(step.domain)

    def test_multiply_back_for_random_polys(self):
        texts = [
            "2*a + 4*b + 6*c + 8*d",
            "1/2*x*y - 3/7*y^2 + 5*x",
            "a*b*c + a*b + a*c + b*c",
            "x^3 + 2*x^2 + 4*x + 8",
        ]
        for text in texts:
            result = factor_polynomial(text)
            self.assertTrue(result.verified, text)
            self.assertTrue(result.exact, text)
            expanded = result.search.dag.expand(result.entry.node,
                                                result.search.variables)
            self.assertEqual(expanded, result.poly)

    def test_budget_truncation_keeps_bounds(self):
        big = parse("(x + y + z)*(u + v + w)*(p + q + r)")
        self.assertEqual(len(big.terms), 27)
        result = factor_polynomial(big, budget=100)
        self.assertFalse(result.exact)
        self.assertLessEqual(result.lower, result.cost)
        if result.entry.node is not None:
            expanded = result.search.dag.expand(result.entry.node,
                                                result.search.variables)
            self.assertEqual(expanded, big)  # 上界表达式仍然合法

    def test_zero_budget(self):
        result = factor_polynomial("x + y + z", budget=0)
        self.assertFalse(result.exact)
        self.assertIsNone(result.entry.node)
        self.assertGreaterEqual(result.lower, 2)  # ceil(log2 3)

    def test_no_greedy_global_claim(self):
        # 局部最优(单项直接构造)不等于全局最优: 搜索必须比较所有候选
        result = factor_polynomial("2*x + 2*y + 2*z + 2*w")
        self.assertTrue(result.exact)
        self.assertEqual(result.cost, 2)  # 2*(w + x + y + z): 1 个 add 节点 + 1 个 mul 节点
        self.assertTrue(result.verified)


if __name__ == "__main__":
    unittest.main()
