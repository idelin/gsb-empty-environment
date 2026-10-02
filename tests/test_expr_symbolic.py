"""Expression interpreter and symbolic cross-check tests."""
import math
import unittest

import dualtag as dt
from dualtag import expr, symbolic


class TestExpressionInterpreter(unittest.TestCase):
    def test_parse_and_evaluate(self):
        ast = expr.parse("x^3 + 2*sin(y) - -3")
        self.assertAlmostEqual(
            expr.evaluate(ast, {"x": 2.0, "y": 0.5}),
            8.0 + 2.0 * math.sin(0.5) + 3.0)
        self.assertEqual(expr.variables(ast), {"x", "y"})

    def test_operator_precedence(self):
        self.assertAlmostEqual(expr.evaluate(expr.parse("-2^2"), {}), -4.0)
        self.assertAlmostEqual(expr.evaluate(expr.parse("2^-3"), {}), 0.125)
        self.assertAlmostEqual(expr.evaluate(expr.parse("2^3^2"), {}), 512.0)

    def test_errors(self):
        with self.assertRaises(NameError):
            expr.evaluate(expr.parse("x + 1"), {})
        with self.assertRaises(SyntaxError):
            expr.parse("x +")
        with self.assertRaises(NameError):
            expr.parse("nosuchfn(1)")

    def test_differentiate_through_interpreter(self):
        ast = expr.parse("x^3 + 2*sin(y)")

        def fn(v):
            return expr.evaluate(ast, {"x": v[0], "y": v[1]})

        _, grad = dt.gradient(fn, [2.0, 0.5])
        self.assertAlmostEqual(grad[0], 12.0)
        self.assertAlmostEqual(grad[1], 2.0 * math.cos(0.5))


class TestSymbolicCrossCheck(unittest.TestCase):
    def test_symbolic_matches_ad_on_composed_expression(self):
        ast = expr.parse("x^3 * y + x * sin(y) - exp(x) / (1 + y^2)")
        env = {"x": 1.3, "y": 0.7}
        for var in ("x", "y"):
            sym = symbolic.simplify(symbolic.diff(ast, var))
            ref = expr.evaluate(sym, env)
            _, grad = dt.gradient(
                lambda v: expr.evaluate(ast, {"x": v[0], "y": v[1]}),
                [env["x"], env["y"]])
            got = grad[0] if var == "x" else grad[1]
            self.assertAlmostEqual(got, ref, places=10)

    def test_symbolic_second_derivative(self):
        ast = expr.parse("sin(x) * x^2")
        d2 = symbolic.simplify(symbolic.diff(symbolic.diff(ast, "x"), "x"))
        x0 = 0.9
        ref = expr.evaluate(d2, {"x": x0})
        _, _, d2_ad = dt.second_derivative(
            lambda x: dt.sin(x) * x ** 2, x0)
        self.assertAlmostEqual(d2_ad, ref, places=10)

    def test_symbolic_refuses_nonsmooth(self):
        with self.assertRaises(dt.NonDifferentiableError):
            symbolic.diff(expr.parse("abs(x)"), "x")
        with self.assertRaises(dt.NonDifferentiableError):
            symbolic.diff(expr.parse("min(x, 1)"), "x")

    def test_to_source_roundtrip(self):
        ast = expr.parse("x^3 * y + x * sin(y)")
        sym = symbolic.simplify(symbolic.diff(ast, "x"))
        reparsed = expr.parse(symbolic.to_source(sym))
        env = {"x": 1.1, "y": 2.2}
        self.assertAlmostEqual(expr.evaluate(sym, env),
                               expr.evaluate(reparsed, env))


if __name__ == "__main__":
    unittest.main()
