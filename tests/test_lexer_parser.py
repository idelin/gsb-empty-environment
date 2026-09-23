"""词法 / 语法错误与有理字面量边界。"""

import unittest

from exprlib import FRAC_SLASH, LexError, ParseError, parse


class LexerParserErrorTest(unittest.TestCase):
    def test_float_literal_rejected(self):
        with self.assertRaises(LexError):
            parse("1.5")
        with self.assertRaises(LexError):
            parse("1" + FRAC_SLASH + "2.0")

    def test_unknown_character(self):
        with self.assertRaises(LexError):
            parse("a $ b")

    def test_zero_denominator_literal(self):
        with self.assertRaises(LexError):
            parse("1" + FRAC_SLASH + "0")

    def test_unbalanced_parentheses(self):
        with self.assertRaises(ParseError):
            parse("(a + b")
        with self.assertRaises(ParseError):
            parse("a + b)")

    def test_dangling_operator(self):
        with self.assertRaises(ParseError):
            parse("a + ")
        with self.assertRaises(ParseError):
            parse("* a")

    def test_bad_call(self):
        with self.assertRaises(ParseError):
            parse("(a+b)(c)")
        with self.assertRaises(ParseError):
            parse("f(a,)")
        with self.assertRaises(ParseError):
            parse("f(a b)")

    def test_bad_conditional(self):
        with self.assertRaises(ParseError):
            parse("a ? b")
        with self.assertRaises(ParseError):
            parse("a ? b : ")

    def test_empty_input(self):
        with self.assertRaises(ParseError):
            parse("")

    def test_division_is_operator_fraction_is_literal(self):
        from exprlib.ast import BinOp, Rat
        self.assertEqual(parse("1 / 2"),
                         BinOp("/", Rat(1), Rat(2)))
        self.assertEqual(parse("1" + FRAC_SLASH + "2"), Rat(1, 2))


if __name__ == "__main__":
    unittest.main()
