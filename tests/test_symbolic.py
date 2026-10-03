import unittest

from dualtag import (Interpreter, compile_expression, derivative,
                     symbolic_derivative)
from dualtag.errors import NonDifferentiableError


class TestSymbolicCrossCheck(unittest.TestCase):
    """AD results must match evaluating the symbolic derivative."""

    def _check(self, source, env):
        params = sorted(env)
        f = compile_expression(source, params)
        for i, var in enumerate(params):
            sym = symbolic_derivative(source, var)
            want = Interpreter(env).eval(sym)
            args = [env[p] for p in params]
            got = derivative(
                lambda v, i=i: f(*args[:i], v, *args[i + 1:]), args[i])
            self.assertAlmostEqual(got, want, places=6,
                                   msg=f"d/d{var} of {source}: AD={got} sym={want}")

    def test_polynomial(self):
        self._check("x**3", {"x": 1.7})
        self._check("x**3 - 2*x*y + y**2", {"x": 1.2, "y": 0.4})

    def test_transcendental(self):
        self._check("sin(x)*x + exp(y)", {"x": 0.6, "y": 0.3})
        self._check("cos(x*y) + tan(x)", {"x": 0.4, "y": 0.9})
        self._check("log(x) + sqrt(x*y)", {"x": 1.5, "y": 0.7})

    def test_rational(self):
        self._check("x*y/(x + y)", {"x": 1.2, "y": 2.3})
        self._check("x**y", {"x": 1.5, "y": 2.5})

    def test_symbolic_string_sanity(self):
        self.assertEqual(symbolic_derivative("x**3", "x"), "3.0 * x ** 2.0")
        self.assertEqual(symbolic_derivative("x + y", "x"), "1.0")

    def test_non_smooth_rejected(self):
        with self.assertRaises(NonDifferentiableError):
            symbolic_derivative("abs(x)", "x")
        with self.assertRaises(NonDifferentiableError):
            symbolic_derivative("min(x, y)", "x")
        with self.assertRaises(NonDifferentiableError):
            symbolic_derivative("x if x > 0 else y", "x")


if __name__ == "__main__":
    unittest.main()
