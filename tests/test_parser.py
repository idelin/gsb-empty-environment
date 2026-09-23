"""解析器行为：优先级、结合性、负号与幂、条件表达式、调用、字面量。"""
import unittest
from fractions import Fraction

from minparen import (Binary, Call, Cond, Const, ParseError, Unary, Var,
                      parse)


class ParserTests(unittest.TestCase):
    def test_minus_left_assoc(self):
        self.assertEqual(
            parse("a - b - c"),
            Binary("-", Binary("-", Var("a"), Var("b")), Var("c")),
        )

    def test_power_right_assoc(self):
        self.assertEqual(
            parse("2^3^2"),
            Binary("^", Const(2), Binary("^", Const(3), Const(2))),
        )

    def test_unary_minus_binds_looser_than_power(self):
        # -x^2 == -(x^2)
        self.assertEqual(
            parse("-x^2"),
            Unary("-", Binary("^", Var("x"), Const(2))),
        )

    def test_parenthesized_unary_base(self):
        # (-x)^2 与 -x^2 结构不同
        self.assertEqual(
            parse("(-x)^2"),
            Binary("^", Unary("-", Var("x")), Const(2)),
        )
        self.assertNotEqual(parse("(-x)^2"), parse("-x^2"))

    def test_unary_after_power_needs_parens(self):
        with self.assertRaises(ParseError):
            parse("2^-1")
        self.assertEqual(
            parse("2^(-1)"),
            Binary("^", Const(2), Unary("-", Const(1))),
        )

    def test_unary_operand_of_times(self):
        self.assertEqual(
            parse("-a*b"),
            Binary("*", Unary("-", Var("a")), Var("b")),
        )
        self.assertEqual(
            parse("a*-b"),
            Binary("*", Var("a"), Unary("-", Var("b"))),
        )

    def test_precedence_levels(self):
        self.assertEqual(
            parse("a+b*c"),
            Binary("+", Var("a"), Binary("*", Var("b"), Var("c"))),
        )

    def test_cond_right_assoc(self):
        self.assertEqual(
            parse("a?b:c?d:e"),
            Cond(Var("a"), Var("b"), Cond(Var("c"), Var("d"), Var("e"))),
        )

    def test_cond_condition_needs_parens(self):
        self.assertEqual(
            parse("(a?b:c)?d:e"),
            Cond(Cond(Var("a"), Var("b"), Var("c")), Var("d"), Var("e")),
        )

    def test_cond_middle_is_full_expr(self):
        self.assertEqual(
            parse("a?b?c:d:e"),
            Cond(Var("a"), Cond(Var("b"), Var("c"), Var("d")), Var("e")),
        )

    def test_call(self):
        self.assertEqual(
            parse("f(a+b, g(c))"),
            Call("f", (Binary("+", Var("a"), Var("b")),
                       Call("g", (Var("c"),)))),
        )

    def test_call_zero_args(self):
        self.assertEqual(parse("f()"), Call("f", ()))

    def test_rational_literal_exact(self):
        self.assertEqual(parse("0.1"), Const(Fraction(1, 10)))
        self.assertEqual(parse("2.50"), Const(Fraction(5, 2)))
        self.assertEqual(parse("0.125"), Const(Fraction(1, 8)))

    def test_const_rejects_float(self):
        with self.assertRaises(TypeError):
            Const(0.1)

    def test_trailing_garbage_rejected(self):
        with self.assertRaises(ParseError):
            parse("a b")

    def test_empty_rejected(self):
        with self.assertRaises(ParseError):
            parse("")


if __name__ == "__main__":
    unittest.main()
