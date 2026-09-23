"""关键结构样例：解析 -> 最小打印 -> 再解析，树必须相等。"""

import unittest

from exprlib import FRAC_SLASH, parse, to_string
from exprlib.ast import BinOp, Call, Cond, Neg, Rat, Var


def F(n, d=1):
    return Rat(n, d)


class RoundTripTest(unittest.TestCase):
    def assertMinimal(self, text, expected):
        tree = parse(text)
        printed = to_string(tree)
        self.assertEqual(printed, expected)
        self.assertEqual(parse(printed), tree, f"往返失败：{text} -> {printed}")

    def test_subtraction_is_structural_not_numeric(self):
        # a-(b-c) 绝不能打印成 a-b-c（不同的树）。
        self.assertMinimal("a-(b-c)", "a - (b - c)")
        self.assertNotEqual(parse("a-(b-c)"), parse("a-b-c"))
        self.assertMinimal("a-b-c", "a - b - c")

    def test_power_vs_unary_minus(self):
        self.assertMinimal("(-x)^2", "(-x) ^ 2")
        self.assertMinimal("-x^2", "-x ^ 2")
        self.assertNotEqual(parse("(-x)^2"), parse("-x^2"))
        self.assertMinimal("x^-2", "x ^ -2")
        self.assertMinimal("2^3^2", "2 ^ 3 ^ 2")
        self.assertMinimal("(2^3)^2", "(2 ^ 3) ^ 2")

    def test_exact_rational_literals(self):
        third = "1" + FRAC_SLASH + "3"
        self.assertEqual(parse(third), Rat(1, 3))
        self.assertMinimal(third, third)
        self.assertMinimal(third + " + " + third + " + " + third,
                           third + " + " + third + " + " + third)
        self.assertMinimal("(-2" + FRAC_SLASH + "3)^2",
                           "(-2" + FRAC_SLASH + "3) ^ 2")
        self.assertMinimal("2" + FRAC_SLASH + "4", "1" + FRAC_SLASH + "2")
        self.assertEqual(to_string(Rat(5)), "5")
        self.assertEqual(to_string(Rat(-3, 2)), "-3" + FRAC_SLASH + "2")

    def test_conditional(self):
        self.assertMinimal("a?b:c?d:e", "a ? b : c ? d : e")
        self.assertMinimal("(a?b:c)?d:e", "(a ? b : c) ? d : e")
        self.assertMinimal("a?b+c:d", "a ? b + c : d")
        self.assertMinimal("a?b:c+d", "a ? b : c + d")
        self.assertEqual(parse("a?b:c?d:e"),
                         Cond(Var("a"), Var("b"),
                              Cond(Var("c"), Var("d"), Var("e"))))

    def test_calls(self):
        self.assertMinimal("f(a+b, c)", "f(a + b, c)")
        self.assertMinimal("f(g(a+b), c?d:e)", "f(g(a + b), c ? d : e)")
        self.assertMinimal("f()", "f()")
        self.assertEqual(parse("f(x)"), Call("f", (Var("x"),)))

    def test_unary_minus_contexts(self):
        self.assertMinimal("--x", "--x")
        self.assertMinimal("-(a*b)", "-(a * b)")
        self.assertMinimal("-(a+b)", "-(a + b)")
        self.assertMinimal("a*-(b+c)", "a * -(b + c)")
        self.assertMinimal("x^-(a+b)", "x ^ -(a + b)")
        self.assertMinimal("-(a?b:c)", "-(a ? b : c)")

    def test_explicit_ast_printing(self):
        self.assertEqual(to_string(BinOp("-", Var("a"),
                                         BinOp("-", Var("b"), Var("c")))),
                         "a - (b - c)")
        self.assertEqual(to_string(BinOp("^", Neg(Var("x")), F(2))),
                         "(-x) ^ 2")
        self.assertEqual(to_string(Neg(BinOp("^", Var("x"), F(2)))), "-x ^ 2")


if __name__ == "__main__":
    unittest.main()
