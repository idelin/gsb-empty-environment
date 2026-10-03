"""Analytic and finite-difference validation of the differentiation engine."""
import math
import unittest

from dualtag import (active_tags, derivative, gradient, hessian, jacobian,
                     jvp, mixed_directional, mixed_partial, second_derivative)
from dualtag import dual as D
from dualtag.errors import DomainError, NonDifferentiableError


def central_diff(f, x, h=1e-5):
    return (f(x + h) - f(x - h)) / (2.0 * h)


def central_diff2(f, x, h=1e-3):
    return (f(x + h) - 2.0 * f(x) + f(x - h)) / (h * h)


class TestCubeAnalytic(unittest.TestCase):
    """f(x) = x**3: f' = 3x^2, f'' = 6x, mixed directional = 6x*u*v."""

    def test_first_derivative(self):
        for x in (-2.0, 0.0, 1.5, 3.0):
            self.assertAlmostEqual(derivative(lambda v: v ** 3, x), 3.0 * x * x,
                                   places=9)

    def test_second_derivative(self):
        for x in (-1.5, 0.0, 2.0):
            self.assertAlmostEqual(second_derivative(lambda v: v ** 3, x),
                                   6.0 * x, places=6)

    def test_mixed_directional(self):
        x, u, v = 2.0, 3.0, -0.5
        got = mixed_directional(lambda t: t ** 3, x, u, v)
        self.assertAlmostEqual(got, 6.0 * x * u * v, places=6)

    def test_mixed_partial_two_vars(self):
        f = lambda x, y: x ** 3 * y ** 2
        # d2/dxdy = 6 x^2 y, symmetric
        self.assertAlmostEqual(mixed_partial(f, (2.0, 3.0), 0, 1), 72.0, places=6)
        self.assertAlmostEqual(mixed_partial(f, (2.0, 3.0), 1, 0), 72.0, places=6)
        # d2/dx2 = 6 x y^2 ; d2/dy2 = 2 x^3
        self.assertAlmostEqual(mixed_partial(f, (2.0, 3.0), 0, 0), 108.0, places=6)
        self.assertAlmostEqual(mixed_partial(f, (2.0, 3.0), 1, 1), 16.0, places=6)


class TestNestingNoConfusion(unittest.TestCase):
    """Inner differentiation referencing outer variables must not pick up
    the outer perturbation (perturbation confusion)."""

    def test_classic_confusion_case(self):
        # d/dx [ x * (d/dy (x + y)) ] == d/dx [ x * 1 ] == 1.
        # A confused implementation answers 2.
        def outer(x):
            return x * derivative(lambda y: x + y, 5.0)
        self.assertAlmostEqual(derivative(outer, 3.0), 1.0, places=12)

    def test_inner_references_outer_variable(self):
        # inner = d/dy (x*y) = x, so outer = d/dx x^2 = 2x.
        def outer(x):
            return x * derivative(lambda y: x * y, 2.0)
        self.assertAlmostEqual(derivative(outer, 3.0), 6.0, places=9)

    def test_triple_nesting(self):
        def outer(x):
            return x * derivative(
                lambda y: y * derivative(lambda z: x + y + z, 1.0), 2.0)
        self.assertAlmostEqual(derivative(outer, 4.0), 1.0, places=9)

    def test_nested_second_derivative_via_derivative_calls(self):
        # inner: d/dy (x y)^2 at y=1 -> 2 x^2 ; outer: d/dx 2x^2 = 4x = 8
        d2 = derivative(lambda x: derivative(lambda y: (x * y) ** 2, 1.0), 2.0)
        self.assertAlmostEqual(d2, 8.0, places=9)


class TestMultiDirection(unittest.TestCase):
    def test_jvp_multiple_directions(self):
        primal, tangents = jvp(lambda x: x ** 3, 2.0, (1.0, 2.0, -1.0))
        self.assertAlmostEqual(primal, 8.0)
        self.assertEqual(len(tangents), 3)
        for d, t in zip((1.0, 2.0, -1.0), tangents):
            self.assertAlmostEqual(t, 12.0 * d, places=9)

    def test_jacobian_uses_multi_direction_seed(self):
        f = lambda v: [v[0] * v[1], v[0] + v[1] ** 2]
        primals, J = jacobian(f, [2.0, 3.0])
        self.assertEqual(primals, [6.0, 11.0])
        self.assertEqual(J, [[3.0, 2.0], [1.0, 6.0]])

    def test_gradient_and_hessian(self):
        value, g = gradient(lambda x, y: x * x * y + D.sin(y), (2.0, 0.5))
        self.assertAlmostEqual(value, 2.0 + math.sin(0.5))
        self.assertAlmostEqual(g[0], 2.0)
        self.assertAlmostEqual(g[1], 4.0 + math.cos(0.5))

        H = hessian(lambda x, y: x ** 3 + x * y, (1.0, 2.0))
        self.assertAlmostEqual(H[0][0], 6.0)
        self.assertAlmostEqual(H[0][1], 1.0)
        self.assertAlmostEqual(H[1][0], 1.0)
        self.assertAlmostEqual(H[1][1], 0.0)


class TestFiniteDifference(unittest.TestCase):
    def test_first_derivative_matches_fd(self):
        f = lambda x: D.sin(x) * x + D.exp(-x * 0.5)
        for x in (0.3, 1.2, 2.7):
            self.assertAlmostEqual(derivative(f, x), central_diff(f, x), places=4)

    def test_second_derivative_matches_fd(self):
        f = lambda x: x ** 3 - 2.0 * D.cos(x)
        for x in (0.7, 1.9):
            self.assertAlmostEqual(second_derivative(f, x),
                                   central_diff2(f, x), places=2)

    def test_mixed_directional_matches_fd(self):
        f = lambda t: D.sin(t) * t ** 2
        x, u, v = 1.3, 0.7, -1.1
        h = 1e-4
        fd = (f(x + h * u + h * v) - f(x + h * u - h * v)
              - f(x - h * u + h * v) + f(x - h * u - h * v)) / (4 * h * h)
        self.assertAlmostEqual(mixed_directional(f, x, u, v), fd, places=3)


class TestTagHygiene(unittest.TestCase):
    """Cancellation / recovery must not leak tag state."""

    def test_tags_clean_after_success(self):
        derivative(lambda x: x ** 2, 1.0)
        self.assertEqual(active_tags(), ())

    def test_tags_clean_after_domain_error(self):
        with self.assertRaises(DomainError):
            derivative(lambda x: D.log(x), -1.0)
        self.assertEqual(active_tags(), ())
        # recovery: a fresh computation is unaffected by the aborted one
        self.assertAlmostEqual(derivative(lambda x: x * x, 3.0), 6.0, places=9)

    def test_tags_clean_after_non_differentiable(self):
        with self.assertRaises(NonDifferentiableError):
            derivative(lambda x: D.dual_abs(x), 0.0)
        self.assertEqual(active_tags(), ())

    def test_tags_clean_after_nested_abort(self):
        def outer(x):
            inner = derivative(lambda y: D.log(y), -1.0)  # raises
            return x * inner
        with self.assertRaises(DomainError):
            derivative(outer, 2.0)
        self.assertEqual(active_tags(), ())
        self.assertAlmostEqual(second_derivative(lambda v: v ** 3, 2.0), 12.0,
                               places=6)


if __name__ == "__main__":
    unittest.main()
