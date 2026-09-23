import unittest
from fractions import Fraction

from rational_id.parser import ParseError, UnsupportedError, parse


class ParserTest(unittest.TestCase):
    def eval(self, src, env=None):
        return parse(src).eval(env or {})

    def test_precedence(self):
        self.assertEqual(self.eval("1+2*3"), Fraction(7))
        self.assertEqual(self.eval("(1+2)*3"), Fraction(9))
        self.assertEqual(self.eval("2^3*4"), Fraction(32))
        self.assertEqual(self.eval("10/4"), Fraction(5, 2))

    def test_exact_rational_arithmetic(self):
        self.assertEqual(self.eval("0.1+0.2"), Fraction(3, 10))
        self.assertEqual(self.eval("1/3+1/6"), Fraction(1, 2))

    def test_unary_minus(self):
        self.assertEqual(self.eval("-2^2"), Fraction(-4))
        self.assertEqual(self.eval("(-2)^2"), Fraction(4))
        self.assertEqual(self.eval("-x+1", {"x": Fraction(1)}), Fraction(0))

    def test_variables(self):
        self.assertEqual(self.eval("x^2+2*x+1", {"x": Fraction(3)}), Fraction(16))

    def test_division_by_zero(self):
        with self.assertRaises(ZeroDivisionError):
            self.eval("1/(x-x)", {"x": Fraction(5)})

    def test_unsupported_constructs(self):
        for src in ("sin(x)", "sqrt(2)", "x^-1", "x^y", "x^0.5", "x^(1+1)"):
            with self.assertRaises(UnsupportedError, msg=src):
                parse(src)

    def test_parse_errors(self):
        for src in ("x +", "(x", "2x", "", "x @ 1"):
            with self.assertRaises(ParseError, msg=src):
                parse(src)


if __name__ == "__main__":
    unittest.main()
