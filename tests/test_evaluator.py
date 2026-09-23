"""精确有理求值与各运算适用域。"""

import unittest
from fractions import Fraction

from exprlib import DomainError, EvalError, FRAC_SLASH, evaluate, parse


class EvaluatorTest(unittest.TestCase):
    def test_no_float_loss(self):
        text = " + ".join(["1" + FRAC_SLASH + "3"] * 3)
        value = evaluate(parse(text))
        self.assertIsInstance(value, Fraction)
        self.assertEqual(value, Fraction(1))

    def test_arithmetic(self):
        self.assertEqual(evaluate(parse("2 * 3 + 4")), Fraction(10))
        self.assertEqual(
            evaluate(parse("(1" + FRAC_SLASH + "2 + 1" + FRAC_SLASH + "4) * 2")),
            Fraction(3, 2),
        )
        self.assertEqual(evaluate(parse("(-2)^3")), Fraction(-8))
        self.assertEqual(evaluate(parse("2^-2")), Fraction(1, 4))

    def test_division_by_zero_domain(self):
        with self.assertRaises(DomainError):
            evaluate(parse("1 / 0"))
        with self.assertRaises(DomainError):
            evaluate(parse("0^-1"))

    def test_non_integer_power_domain(self):
        with self.assertRaises(DomainError):
            evaluate(parse("2^(1" + FRAC_SLASH + "2)"))

    def test_conditional_short_circuit(self):
        self.assertEqual(
            evaluate(parse("x ? 1 / x : 42"), env={"x": 0}),
            Fraction(42),
        )
        self.assertEqual(
            evaluate(parse("x ? 1 / x : 42"), env={"x": 2}),
            Fraction(1, 2),
        )

    def test_unbound_symbol_and_function(self):
        with self.assertRaises(EvalError):
            evaluate(parse("y + 1"))
        with self.assertRaises(EvalError):
            evaluate(parse("h(1)"))

    def test_functions(self):
        funcs = {
            "sq": lambda x: x * x,
            "add": lambda x, y: x + y,
        }
        self.assertEqual(evaluate(parse("sq(3" + FRAC_SLASH + "2)"),
                                  functions=funcs), Fraction(9, 4))
        self.assertEqual(evaluate(parse("add(sq(2), 3)"), functions=funcs),
                         Fraction(7))

    def test_large_exact_value(self):
        self.assertEqual(evaluate(parse("2^64")), Fraction(1 << 64))


if __name__ == "__main__":
    unittest.main()
