"""恒等判断：等价、非等价（含反例复核）、定义域、预算、支持域。"""

import unittest
from fractions import Fraction

from ratid.judge import Verdict, judge
from ratid.normalize import normalize_expression
from ratid.polynomial import eval_poly


class TestEquivalent(unittest.TestCase):
    def assertEquivalent(self, a, b, budget=256):
        result = judge(a, b, budget=budget)
        self.assertIs(result.verdict, Verdict.EQUIVALENT, msg=result.reason)
        self.assertIsNone(result.counterexample)

    def test_classic_identity_same_domain(self):
        self.assertEquivalent("(x^2 - 1)/(x - 1)",
                              "x + 1 + (x - 1)/(x - 1) - 1")

    def test_common_denominator(self):
        self.assertEquivalent("1/x + 1/(x + 1)", "(2*x + 1)/(x*(x + 1))")

    def test_same_excluded_points(self):
        # 约分形式为 1，且两侧都排除 ±1，在 Q 上是同一个部分函数
        self.assertEquivalent("(x^2 - 1)/(x^2 - 1)",
                              "((x - 1)*(x + 1))/((x - 1)*(x + 1))")

    def test_same_excluded_points_with_extra(self):
        self.assertEquivalent("(x - 1)/(x - 1)", "(x - 1)^2/(x - 1)^2")

    def test_negative_power(self):
        self.assertEquivalent("(x - 2)^(-1)", "1/(x - 2)")
        self.assertEquivalent("x^-2", "1/x^2")

    def test_rational_constants(self):
        self.assertEquivalent("0.5*x + 0.25", "(2*x + 1)/4")

    def test_higher_degree_expansion(self):
        self.assertEquivalent("(x + 1)^4",
                              "x^4 + 4*x^3 + 6*x^2 + 4*x + 1")

    def test_irreducible_quadratic_domains_match_over_rationals(self):
        # x^2+1 在 Q 上无零点，两侧定义域都是全部有理数
        self.assertEquivalent("(x^2 + 1)/(x^2 + 1)", "1")


class TestNotEquivalent(unittest.TestCase):
    def assertNotEquivalent(self, a, b, expect_kind=None):
        result = judge(a, b)
        self.assertIs(result.verdict, Verdict.NOT_EQUIVALENT, msg=result.reason)
        self.assertIsNotNone(result.counterexample)
        if expect_kind is not None:
            self.assertEqual(result.counterexample.kind, expect_kind)
        self._independently_verify(a, b, result)
        return result

    def _independently_verify(self, a, b, result):
        """用规范形按“原始除式”独立复核反例。"""
        left, _ = normalize_expression(a)
        right, _ = normalize_expression(b)
        x = result.counterexample.x
        dl = eval_poly(left.restrictions, x) != 0
        dr = eval_poly(right.restrictions, x) != 0
        ce = result.counterexample
        self.assertEqual((dl, dr), (ce.defined_left, ce.defined_right))
        if ce.kind == "value_diff":
            self.assertTrue(dl and dr)
            vl = eval_poly(left.numerator, x) / eval_poly(left.denominator, x)
            vr = eval_poly(right.numerator, x) / eval_poly(right.denominator, x)
            self.assertEqual(vl, ce.value_left)
            self.assertEqual(vr, ce.value_right)
            self.assertNotEqual(vl, vr)

    def test_value_difference_integer_point(self):
        result = self.assertNotEquivalent("(x^2 - 1)/(x - 1)", "x", "value_diff")
        # x=0 处左侧 1，右侧 0（搜索从最小的点开始）
        self.assertEqual(result.counterexample.x, Fraction(0))

    def test_value_difference_polynomials(self):
        self.assertNotEquivalent("x^2", "x", "value_diff")

    def test_domain_difference_cancellation(self):
        result = self.assertNotEquivalent(
            "(x^2 - 1)/(x - 1)", "x + 1", "domain_diff")
        self.assertEqual(result.counterexample.x, Fraction(1))
        self.assertFalse(result.counterexample.defined_left)
        self.assertTrue(result.counterexample.defined_right)

    def test_domain_difference_extra_factor(self):
        result = self.assertNotEquivalent(
            "(x^2 - 1)/(x - 1)",
            "((x^2 - 1)*(x + 1))/((x - 1)*(x + 1))",
            "domain_diff",
        )
        self.assertEqual(result.counterexample.x, Fraction(-1))

    def test_zero_subtraction_still_excludes_point(self):
        # 1/x - 1/x 形式上约分是 0，但 x=0 仍无定义
        result = self.assertNotEquivalent("1/x - 1/x", "0", "domain_diff")
        self.assertEqual(result.counterexample.x, Fraction(0))

    def test_fractional_counterexample_possible(self):
        # 右侧比同一个有理函数多除一次 (2x-1)，分数点 x=1/2 仅左侧有定义
        result = self.assertNotEquivalent(
            "(x - 1)/(x - 1)",
            "((x - 1)*(2*x - 1))/((x - 1)*(2*x - 1))",
            "domain_diff",
        )
        self.assertEqual(result.counterexample.x, Fraction(1, 2))
        self.assertTrue(result.counterexample.defined_left)
        self.assertFalse(result.counterexample.defined_right)

class TestUnknown(unittest.TestCase):
    def test_budget_exceeded(self):
        result = judge("(1 + x)^20", "x^20 + 20*x^19 + 190*x^18", budget=20)
        self.assertIs(result.verdict, Verdict.UNKNOWN)
        self.assertIn("预算", result.reason)

    def test_budget_blocks_large_equivalent_too(self):
        # 真恒等但预算不足以展开时必须标未知，禁止猜测
        result = judge("(1 + x)^30",
                        " + ".join(f"c{i}*x^{i}" for i in range(31)),
                        budget=8)
        self.assertIs(result.verdict, Verdict.UNKNOWN)

    def test_second_variable(self):
        result = judge("x + y", "x")
        self.assertIs(result.verdict, Verdict.UNKNOWN)
        self.assertIn("支持域", result.reason)

    def test_non_integer_exponent(self):
        result = judge("x^(1/2)", "x")
        self.assertIs(result.verdict, Verdict.UNKNOWN)

    def test_division_by_zero(self):
        result = judge("1/0", "1")
        self.assertIs(result.verdict, Verdict.UNKNOWN)

    def test_implicit_multiplication(self):
        result = judge("2x", "2*x")
        self.assertIs(result.verdict, Verdict.UNKNOWN)

    def test_zero_to_zero(self):
        result = judge("0^0", "1")
        self.assertIs(result.verdict, Verdict.UNKNOWN)


class TestStepsAndDomains(unittest.TestCase):
    def test_every_step_carries_domain_text(self):
        result = judge("1/x + 1/(x+1)", "(2*x + 1)/(x*(x + 1))")
        self.assertTrue(result.steps)
        for step in result.steps:
            self.assertTrue(step.stage and step.expr and step.detail)
            self.assertIn("适用域", step.detail)

    def test_excluded_points_recorded(self):
        result = judge("1/(x*(x + 1))", "1")
        self.assertEqual(set(result.excluded_left), {Fraction(0), Fraction(-1)})
        self.assertEqual(result.excluded_right, {})


if __name__ == "__main__":
    unittest.main()


class TestCounterexamplesIndependentlyFromAST(unittest.TestCase):
    """不使用判定器的规范形，直接对 AST 递归求值，独立验证所有内置反例。"""

    def _eval_ast(self, node, x):
        from fractions import Fraction as F
        k = node.kind
        if k == "num":
            return node.value
        if k == "var":
            return x
        if k == "neg":
            v = self._eval_ast(node.left, x)
            return None if v is None else -v
        if k == "pow":
            v = self._eval_ast(node.left, x)
            if v is None:
                return None
            n = int(node.value)
            if n < 0:
                if v == 0:
                    return None
                return F(1) / v ** (-n)
            return v ** n
        a = self._eval_ast(node.left, x)
        b = self._eval_ast(node.right, x)
        if a is None or b is None:
            return None
        if k == "add":
            return a + b
        if k == "sub":
            return a - b
        if k == "mul":
            return a * b
        if k == "div":
            return None if b == 0 else a / b
        raise AssertionError(k)

    def _defined_and_value(self, text, x):
        from ratid.parser import parse
        value = self._eval_ast(parse(text), x)
        return (value is not None, value)

    CASES = [
        ("(x^2 - 1)/(x - 1)", "x"),
        ("(x^2 - 1)/(x - 1)", "x + 1"),
        ("(x^2 - 1)/(x - 1)",
         "((x^2 - 1)*(x + 1))/((x - 1)*(x + 1))"),
        ("1/x - 1/x", "0"),
        ("(x - 1)/(x - 1)",
         "((x - 1)*(2*x - 1))/((x - 1)*(2*x - 1))"),
    ]

    def test_all_counterexamples_hold_under_independent_eval(self):
        from ratid.judge import judge as j
        for a, b in self.CASES:
            result = j(a, b)
            self.assertIs(result.verdict, Verdict.NOT_EQUIVALENT, msg=a)
            ce = result.counterexample
            dl, vl = self._defined_and_value(a, ce.x)
            dr, vr = self._defined_and_value(b, ce.x)
            self.assertEqual((dl, dr), (ce.defined_left, ce.defined_right), a)
            if ce.kind == "value_diff":
                self.assertNotEqual(vl, vr, a)
            else:
                self.assertNotEqual(dl, dr, a)
