import math
import unittest

from dualtag import (Interpreter, compile_expression, derivative, eval_expr)
from dualtag.errors import InterpreterError, NonDifferentiableError


class TestEval(unittest.TestCase):
    def test_arithmetic(self):
        self.assertAlmostEqual(eval_expr("x**3 + 2*x - 1", x=2.0), 11.0)
        self.assertAlmostEqual(eval_expr("(x - y) / (x + y)", x=3.0, y=1.0), 0.5)

    def test_functions(self):
        self.assertAlmostEqual(eval_expr("sin(x) + exp(y)", x=0.5, y=0.0),
                               math.sin(0.5) + 1.0)
        self.assertAlmostEqual(eval_expr("sqrt(x) + log(x)", x=4.0),
                               2.0 + math.log(4.0))

    def test_compile_and_differentiate(self):
        f = compile_expression("x**3 - 2*sin(x)", ["x"])
        self.assertAlmostEqual(derivative(f, 0.7),
                               3 * 0.7 ** 2 - 2 * math.cos(0.7), places=9)

    def test_errors(self):
        with self.assertRaises(InterpreterError):
            eval_expr("x + 1", y=2.0)  # unknown name x
        with self.assertRaises(InterpreterError):
            eval_expr("(lambda t: t)(1)")
        with self.assertRaises(InterpreterError):
            eval_expr("1 < x < 3", x=2.0)  # chained comparison
        with self.assertRaises(InterpreterError):
            eval_expr("'string'")


class TestBranches(unittest.TestCase):
    def test_branch_smooth_regions(self):
        f = compile_expression("x*x if x > 1 else x", ["x"])
        self.assertAlmostEqual(derivative(f, 2.0), 4.0, places=9)
        self.assertAlmostEqual(derivative(f, 0.5), 1.0, places=9)

    def test_branch_at_boundary_is_non_differentiable(self):
        f = compile_expression("x*x if x >= 0 else -x", ["x"])
        with self.assertRaises(NonDifferentiableError):
            derivative(f, 0.0)

    def test_branch_without_duals_is_fine(self):
        self.assertEqual(eval_expr("1 if x == 2 else 0", x=2.0), 1)

    def test_abs_kink(self):
        f = compile_expression("abs(x)", ["x"])
        self.assertAlmostEqual(derivative(f, -2.0), -1.0, places=9)
        self.assertAlmostEqual(derivative(f, 2.0), 1.0, places=9)
        with self.assertRaises(NonDifferentiableError):
            derivative(f, 0.0)

    def test_min_max(self):
        fmin = compile_expression("min(x, 2.0)", ["x"])
        self.assertAlmostEqual(derivative(fmin, 1.0), 1.0, places=9)
        self.assertAlmostEqual(derivative(fmin, 3.0), 0.0, places=9)
        with self.assertRaises(NonDifferentiableError):
            derivative(fmin, 2.0)
        fmax = compile_expression("max(x, 0.0)", ["x"])
        self.assertAlmostEqual(derivative(fmax, -1.0), 0.0, places=9)


if __name__ == "__main__":
    unittest.main()
