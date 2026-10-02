"""JVP, higher-order and mixed derivatives, perturbation-confusion tests."""
import math
import unittest

import dualtag as dt
from dualtag import expr, numeric, symbolic


class TestXCubed(unittest.TestCase):
    """Required check: f(x) = x^3 first/second derivatives, analytic."""

    def test_first_derivative(self):
        value, d1 = dt.derivative(lambda x: x ** 3, 2.0)
        self.assertAlmostEqual(value, 8.0)
        self.assertAlmostEqual(d1, 12.0)  # 3x^2 at x=2

    def test_second_derivative(self):
        value, d1, d2 = dt.second_derivative(lambda x: x ** 3, 2.0)
        self.assertAlmostEqual(value, 8.0)
        self.assertAlmostEqual(d1, 12.0)
        self.assertAlmostEqual(d2, 12.0)  # 6x at x=2

    def test_against_symbolic_and_finite_difference(self):
        ast = expr.parse("x^3")
        d1_ast = symbolic.simplify(symbolic.diff(ast, "x"))
        d2_ast = symbolic.simplify(symbolic.diff(d1_ast, "x"))
        self.assertEqual(symbolic.to_source(d2_ast), "(6.0 * x)")
        for x0 in (0.5, 2.0, 3.25):
            _, d1, d2 = dt.second_derivative(lambda x: x ** 3, x0)
            self.assertAlmostEqual(d1, expr.evaluate(d1_ast, {"x": x0}))
            self.assertAlmostEqual(d2, expr.evaluate(d2_ast, {"x": x0}))
            self.assertAlmostEqual(d1, numeric.fd_derivative(lambda x: x ** 3, x0), places=6)
            self.assertAlmostEqual(d2, numeric.fd_second(lambda x: x ** 3, x0), places=4)


class TestJVP(unittest.TestCase):
    def test_multi_direction_single_pass(self):
        # f(x, y) = (x^3, x*y); J at (2, 3) = [[12, 0], [3, 2]]
        fn = lambda v: [v[0] ** 3, v[0] * v[1]]
        directions = [[1.0, 0.0], [0.0, 1.0], [2.0, -1.0]]
        primals, push = dt.jvp(fn, [2.0, 3.0], directions)
        self.assertEqual(primals, [8.0, 6.0])
        self.assertEqual(push[0], [12.0, 3.0])
        self.assertEqual(push[1], [0.0, 2.0])
        self.assertEqual(push[2], [24.0, 4.0])  # 2*col0 - col1

    def test_jacobian_matches_finite_difference(self):
        fn = lambda v: [v[0] ** 3, v[0] * v[1], dt.sin(v[1])]
        x = [1.3, 0.9]
        _, J = dt.jacobian(fn, x)
        Jfd = numeric.fd_jacobian(fn, x)
        for row_a, row_b in zip(J, Jfd):
            for a, b in zip(row_a, row_b):
                self.assertAlmostEqual(a, b, places=5)

    def test_gradient(self):
        value, grad = dt.gradient(lambda v: v[0] ** 2 * v[1] + v[1], [3.0, 2.0])
        self.assertAlmostEqual(value, 20.0)
        self.assertEqual(grad, [12.0, 10.0])


class TestMixedAndHessian(unittest.TestCase):
    """Required check: mixed directional second derivatives, analytic."""

    def setUp(self):
        # g(x, y) = x^3 y + x sin(y)
        # g_xy = 3x^2 + cos(y), g_xx = 6xy, g_yy = -x sin(y)
        self.ast = expr.parse("x^3 * y + x * sin(y)")
        self.fn = lambda v: expr.evaluate(self.ast, {"x": v[0], "y": v[1]})
        self.x = [1.5, 0.7]

    def test_mixed_partial_analytic(self):
        got = dt.mixed_partial(self.fn, self.x, 0, 1)
        expected = 3 * 1.5 ** 2 + math.cos(0.7)
        self.assertAlmostEqual(got, expected, places=12)

    def test_mixed_partial_matches_symbolic_and_fd(self):
        got = dt.mixed_partial(self.fn, self.x, 0, 1)
        sym = symbolic.simplify(symbolic.diff(symbolic.diff(self.ast, "x"), "y"))
        ref = expr.evaluate(sym, {"x": self.x[0], "y": self.x[1]})
        fd = numeric.fd_mixed(self.fn, self.x, 0, 1)
        self.assertAlmostEqual(got, ref, places=10)
        self.assertAlmostEqual(got, fd, places=5)

    def test_hessian_full_and_symmetric(self):
        value, grad, H = dt.hessian(self.fn, self.x)
        x, y = self.x
        self.assertAlmostEqual(value, x ** 3 * y + x * math.sin(y))
        self.assertAlmostEqual(grad[0], 3 * x * x * y + math.sin(y))
        self.assertAlmostEqual(grad[1], x ** 3 + x * math.cos(y))
        self.assertAlmostEqual(H[0][0], 6 * x * y)
        self.assertAlmostEqual(H[0][1], 3 * x * x + math.cos(y))
        self.assertAlmostEqual(H[1][0], H[0][1])  # symmetry
        self.assertAlmostEqual(H[1][1], -x * math.sin(y))


class TestPerturbationConfusion(unittest.TestCase):
    """Inner derivatives referencing outer perturbed variables must not
    confuse the two perturbations."""

    def test_inner_constant_wrt_inner_variable(self):
        # d/dx [ x * (d/dy (x + y)) ] = d/dx [ x * 1 ] = 1
        def outer(x):
            _, d = dt.derivative(lambda y: x + y, 0.0)
            return x * d
        _, val = dt.derivative(outer, 5.0)
        self.assertAlmostEqual(dt.primal_value(val), 1.0)

    def test_inner_derivative_carries_outer_perturbation(self):
        # d/dx [ x * (d/dy (x * y)) ] = d/dx [ x * x ] = 2x
        def outer(x):
            _, d = dt.derivative(lambda y: x * y, 1.0)
            return x * d
        _, val = dt.derivative(outer, 3.0)
        self.assertAlmostEqual(dt.primal_value(val), 6.0)

    def test_inner_derivative_of_outer_variable_is_tracked(self):
        # d/dx [ d/dy (x * y) ] = 1
        def outer(x):
            _, d = dt.derivative(lambda y: x * y, 2.0)
            return d
        _, val = dt.derivative(outer, 3.0)
        self.assertAlmostEqual(dt.primal_value(val), 1.0)

    def test_same_variable_nested(self):
        # d/dx [ x * (d/dx x^2 evaluated at the outer x) ] = d/dx 2x^2 = 4x
        def outer(x):
            _, d = dt.derivative(lambda z: z * z, x)
            return x * d
        _, val = dt.derivative(outer, 2.0)
        self.assertAlmostEqual(dt.primal_value(val), 8.0)

    def test_confusion_free_through_expression_interpreter(self):
        # Same as above but the inner derivative goes through the
        # pure-expression interpreter, referencing the outer variable.
        ast = expr.parse("x * y")

        def outer(x):
            _, d = dt.derivative(lambda y: expr.evaluate(ast, {"x": x, "y": y}), 1.0)
            return x * d

        _, val = dt.derivative(outer, 3.0)
        self.assertAlmostEqual(dt.primal_value(val), 6.0)

    def test_triple_nesting(self):
        # d/dx d/dy d/dz (x*y*z) = 1
        def fx(x):
            def fy(y):
                def fz(z):
                    return x * y * z
                return dt.derivative(fz, 4.0)[1]
            return dt.derivative(fy, 3.0)[1]
        _, val = dt.derivative(fx, 2.0)
        self.assertAlmostEqual(dt.primal_value(val), 1.0)


if __name__ == "__main__":
    unittest.main()
