"""打印器：最少必要括号的具体用例。"""
import unittest
from fractions import Fraction

from minparen import (Binary, Call, Cond, Const, Unary, Var, parse,
                      render, to_string)

A, B, C, X = Var("a"), Var("b"), Var("c"), Var("x")


class PrinterTests(unittest.TestCase):
    def test_a_minus_b_minus_c_not_ambiguous(self):
        tree = Binary("-", A, Binary("-", B, C))
        self.assertEqual(to_string(tree), "a - (b - c)")
        # 而 a-b-c 是另一棵树，不需要括号
        self.assertEqual(to_string(parse("a - b - c")), "a - b - c")
        self.assertNotEqual(parse("a - b - c"), tree)

    def test_unary_power_distinction(self):
        self.assertEqual(to_string(Binary("^", Unary("-", X), Const(2))),
                         "(-x) ^ 2")
        self.assertEqual(to_string(Unary("-", Binary("^", X, Const(2)))),
                         "-x ^ 2")

    def test_power_right_assoc(self):
        self.assertEqual(
            to_string(Binary("^", Const(2), Binary("^", Const(3), Const(2)))),
            "2 ^ 3 ^ 2",
        )
        self.assertEqual(
            to_string(Binary("^", Binary("^", Const(2), Const(3)), Const(2))),
            "(2 ^ 3) ^ 2",
        )

    def test_mixed_precedence(self):
        self.assertEqual(
            to_string(Binary("*", A, Binary("+", B, C))), "a * (b + c)")
        self.assertEqual(
            to_string(Binary("+", Binary("*", A, B), C)), "a * b + c")

    def test_same_level_different_op_right_child(self):
        # a-(b+c)：右操作数同层，必须加括号（结构不同，非数值等价问题）
        tree = Binary("-", A, Binary("+", B, C))
        self.assertEqual(to_string(tree), "a - (b + c)")
        # a*(b/c)：同层左结合，右操作数必须加括号
        tree2 = Binary("*", A, Binary("/", B, C))
        self.assertEqual(to_string(tree2), "a * (b / c)")

    def test_unary_operand_parens(self):
        self.assertEqual(
            to_string(Unary("-", Binary("*", A, B))), "-(a * b)")
        self.assertEqual(
            to_string(Binary("*", Unary("-", A), B)), "-a * b")

    def test_power_of_negative_needs_parens(self):
        self.assertEqual(
            to_string(Binary("^", Const(2), Unary("-", Const(1)))),
            "2 ^ (-1)")

    def test_cond(self):
        tree = Cond(A, B, Cond(C, Var("d"), Var("e")))
        self.assertEqual(to_string(tree), "a ? b : c ? d : e")
        tree2 = Cond(Cond(A, B, C), Var("d"), Var("e"))
        self.assertEqual(to_string(tree2), "(a ? b : c) ? d : e")
        tree3 = Cond(Binary("+", A, B), C, Var("d"))
        self.assertEqual(to_string(tree3), "a + b ? c : d")

    def test_cond_as_operand_needs_parens(self):
        tree = Binary("+", A, Cond(B, C, Var("d")))
        self.assertEqual(to_string(tree), "a + (b ? c : d)")

    def test_call(self):
        tree = Call("f", (Binary("+", A, B), Call("g", (C,))))
        self.assertEqual(to_string(tree), "f(a + b, g(c))")

    def test_const_formatting(self):
        self.assertEqual(to_string(Const(Fraction(1, 2))), "0.5")
        self.assertEqual(to_string(Const(Fraction(3, 2))), "1.5")
        self.assertEqual(to_string(Const(2)), "2")
        self.assertEqual(to_string(Const(Fraction(1, 20))), "0.05")

    def test_const_domain_errors(self):
        with self.assertRaises(ValueError):
            to_string(Const(Fraction(-1, 2)))  # 负常量须用一元负号表示
        with self.assertRaises(ValueError):
            to_string(Const(Fraction(1, 3)))  # 无有限小数展开

    def test_render_explains_parens(self):
        r = render(parse("a - (b - c)"))
        self.assertEqual(r.text, "a - (b - c)")
        self.assertEqual(len(r.parens), 1)
        self.assertEqual(len(r.reasons), 1)
        self.assertIn("阈值", r.reasons[0])
        i, j = r.parens[0]
        self.assertEqual(r.text[i], "(")
        self.assertEqual(r.text[j], ")")

    def test_double_negation(self):
        tree = Unary("-", Unary("-", X))
        self.assertEqual(to_string(tree), "--x")
        self.assertEqual(parse("--x"), tree)


if __name__ == "__main__":
    unittest.main()
