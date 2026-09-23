"""精确有理数求值：无浮点损失，适用域错误明确。"""
import unittest
from fractions import Fraction

from minparen import EvalError, evaluate, parse


def ev(text, env=None, functions=None):
    return evaluate(parse(text), env=env, functions=functions)


class EvaluateTests(unittest.TestCase):
    def test_decimal_literals_exact(self):
        # 0.1 + 0.2 在浮点下是 0.30000000000000004，这里精确等于 3/10
        result = ev("0.1 + 0.2")
        self.assertIsInstance(result, Fraction)
        self.assertEqual(result, Fraction(3, 10))

    def test_rational_division_exact(self):
        self.assertEqual(ev("1 / 3 * 3"), Fraction(1))
        self.assertEqual(ev("1 / 3"), Fraction(1, 3))

    def test_power(self):
        self.assertEqual(ev("2 ^ 10"), Fraction(1024))
        self.assertEqual(ev("2 ^ (-1)"), Fraction(1, 2))
        self.assertEqual(ev("(-2) ^ 2"), Fraction(4))
        self.assertEqual(ev("-2 ^ 2"), Fraction(-4))  # 与上一行结构不同

    def test_power_domain(self):
        with self.assertRaises(EvalError):
            ev("2 ^ (1 / 2)")  # 非整数指数越出有理数域
        with self.assertRaises(EvalError):
            ev("0 ^ (-1)")

    def test_division_by_zero(self):
        with self.assertRaises(EvalError):
            ev("1 / 0")

    def test_variables(self):
        self.assertEqual(ev("a - (b - c)", env={"a": 5, "b": 3, "c": 1}),
                         Fraction(3))
        self.assertEqual(ev("a - b - c", env={"a": 5, "b": 3, "c": 1}),
                         Fraction(1))

    def test_env_rejects_float(self):
        with self.assertRaises(EvalError):
            ev("a + 1", env={"a": 0.5})

    def test_undefined_variable(self):
        with self.assertRaises(EvalError):
            ev("zzz + 1")

    def test_cond(self):
        self.assertEqual(ev("1 ? 2 : 3"), Fraction(2))
        self.assertEqual(ev("0 ? 2 : 3"), Fraction(3))
        self.assertEqual(ev("a ? a : -a", env={"a": -5}), Fraction(-5))

    def test_call(self):
        funcs = {"f": lambda x, y: x + y, "g": lambda x: x * x}
        self.assertEqual(ev("f(1, 2)", functions=funcs), Fraction(3))
        self.assertEqual(ev("g(1 / 2)", functions=funcs), Fraction(1, 4))

    def test_undefined_function(self):
        with self.assertRaises(EvalError):
            ev("h(1)")


if __name__ == "__main__":
    unittest.main()
