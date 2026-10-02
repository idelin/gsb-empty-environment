"""Implicit-equation solving and differentiation tests."""
import math
import unittest

import dualtag as dt
from dualtag import numeric


def sqrt_branch_residual(x, y):
    # y^2 = x with the y > 0 branch
    return [y[0] ** 2 - x[0]]


class TestImplicitSqrtBranch(unittest.TestCase):
    """Required check: y^2 = x, y > 0 -> dy/dx = 1/(2 sqrt(x))."""

    def test_derivative_at_x4(self):
        dydx, result = dt.implicit_jvp(sqrt_branch_residual, [4.0], [1.0], [[1.0]])
        self.assertTrue(result.converged)
        self.assertFalse(result.jacobian_singular)
        self.assertAlmostEqual(result.y[0], 2.0, places=10)
        self.assertAlmostEqual(dydx[0][0], 0.25, places=10)

    def test_derivative_matches_independent_root_finder(self):
        # Independent validation: bisection (not Newton) + finite difference.
        def root_at(x):
            return numeric.bisect_root(lambda y: y * y - x, 1e-12, 1e6)
        for x0 in (1.0, 4.0, 9.5):
            dydx, _ = dt.implicit_jvp(sqrt_branch_residual, [x0], [0.5 * x0], [[1.0]])
            fd = numeric.fd_derivative(root_at, x0, h=1e-5)
            self.assertAlmostEqual(dydx[0][0], 0.5 / math.sqrt(x0), places=10)
            self.assertAlmostEqual(dydx[0][0], fd, places=5)

    def test_singular_at_zero_fails_explicitly(self):
        # y* = 0 makes J_y = 2y* = 0: must fail, never fake a derivative.
        with self.assertRaises(dt.SingularJacobianError):
            dt.implicit_jvp(sqrt_branch_residual, [0.0], [1.0], [[1.0]])
        # Starting exactly on the singular root hits the pivot test.
        with self.assertRaises(dt.SingularJacobianError):
            dt.implicit_jvp(sqrt_branch_residual, [0.0], [0.0], [[1.0]])

    def test_solve_reports_linear_convergence_as_singular(self):
        result = dt.solve_implicit(sqrt_branch_residual, [0.0], [1.0])
        self.assertTrue(result.converged)          # the root itself is found
        self.assertTrue(result.jacobian_singular)  # ...but it is singular
        self.assertIn("linearly", result.message)

    def test_unconverged_solve_refuses_derivative(self):
        result = dt.solve_implicit(sqrt_branch_residual, [4.0], [100.0], max_iter=1)
        self.assertFalse(result.converged)
        with self.assertRaises(dt.NonConvergenceError):
            dt.implicit_jvp(sqrt_branch_residual, [4.0], [100.0], [[1.0]], max_iter=1)

    def test_nonsmooth_residual_flagged(self):
        # F(x, y) = y - |x|: the branch kink at x = 0 is not differentiable.
        residual = lambda x, y: [y[0] - dt.abs_(x[0])]
        dydx, result = dt.implicit_jvp(residual, [2.0], [0.5], [[1.0]])
        self.assertAlmostEqual(dydx[0][0], 1.0)  # smooth side: dy/dx = 1
        with self.assertRaises(dt.NonDifferentiableError):
            dt.implicit_jvp(residual, [0.0], [0.5], [[1.0]])


class TestImplicitSystem(unittest.TestCase):
    def test_two_by_two_system(self):
        # y0 + y1 = x, y0 * y1 = x  ->  y = ((x+sqrt(x^2-4x))/2, ...)
        # At x = 5: y0 = (5+sqrt5)/2, dy0/dx = (1 + x/sqrt(x^2-4x))/2
        def residual(x, y):
            return [y[0] + y[1] - x[0], y[0] * y[1] - x[0]]
        dydx, result = dt.implicit_jvp(residual, [5.0], [3.0, 1.0], [[1.0]])
        self.assertTrue(result.converged)
        s = math.sqrt(5.0)
        self.assertAlmostEqual(result.y[0], (5.0 + s) / 2.0, places=10)
        self.assertAlmostEqual(result.y[1], (5.0 - s) / 2.0, places=10)
        expected0 = 0.5 * (1.0 + 3.0 / s)   # (1 + (x-2)/sqrt(x^2-4x))/2
        expected1 = 0.5 * (1.0 - 3.0 / s)
        self.assertAlmostEqual(dydx[0][0], expected0, places=9)
        self.assertAlmostEqual(dydx[0][1], expected1, places=9)

    def test_multi_direction_implicit_jvp(self):
        # y^2 = x0 + x1; push both parameter directions at once.
        residual = lambda x, y: [y[0] ** 2 - x[0] - x[1]]
        dydx, result = dt.implicit_jvp(residual, [1.0, 3.0], [1.0],
                                       [[1.0, 0.0], [0.0, 1.0]])
        self.assertTrue(result.converged)
        self.assertAlmostEqual(dydx[0][0], 0.25, places=10)
        self.assertAlmostEqual(dydx[1][0], 0.25, places=10)

    def test_budget_applies_to_solve(self):
        with self.assertRaises(dt.BudgetExceeded):
            dt.solve_implicit(sqrt_branch_residual, [4.0], [1.0],
                              budget=dt.Budget(max_steps=3, max_allocs=10**9))
        # State is clean afterwards: a fresh solve works.
        result = dt.solve_implicit(sqrt_branch_residual, [4.0], [1.0])
        self.assertTrue(result.converged)


if __name__ == "__main__":
    unittest.main()
