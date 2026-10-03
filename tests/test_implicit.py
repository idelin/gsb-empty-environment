import math
import unittest

from dualtag import (implicit_derivative, implicit_jacobian, solve_scalar,
                     solve_system)
from dualtag.errors import NotConvergedError, SingularJacobianError

F_CIRCLE = lambda x, y: y * y - x  # y^2 = x, upper branch y > 0


class TestScalarImplicit(unittest.TestCase):
    def test_upper_branch_derivative(self):
        # y = sqrt(x) => dy/dx = 1/(2 sqrt(x))
        self.assertAlmostEqual(implicit_derivative(F_CIRCLE, 4.0, 1.5), 0.25,
                               places=9)
        self.assertAlmostEqual(implicit_derivative(F_CIRCLE, 9.0, 2.5), 1.0 / 6.0,
                               places=9)

    def test_matches_finite_difference(self):
        h = 1e-6
        fd = (math.sqrt(4.0 + h) - math.sqrt(4.0 - h)) / (2 * h)
        self.assertAlmostEqual(implicit_derivative(F_CIRCLE, 4.0, 1.0), fd,
                               places=6)

    def test_convergence_report(self):
        res = solve_scalar(F_CIRCLE, 4.0, 1.5)
        self.assertTrue(res.converged)
        self.assertFalse(res.singular)
        self.assertAlmostEqual(res.root, 2.0, places=9)
        self.assertLessEqual(res.residual, 1e-12)
        self.assertAlmostEqual(res.jacobian, 4.0, places=9)
        self.assertGreater(res.iterations, 0)

    def test_singular_at_origin(self):
        # y^2 = 0 has a double root at y = 0: dF/dy = 2y = 0 there.
        res = solve_scalar(F_CIRCLE, 0.0, 1.0)
        self.assertTrue(res.singular)
        with self.assertRaises(SingularJacobianError):
            implicit_derivative(F_CIRCLE, 0.0, 1.0)

    def test_no_fake_derivative_when_not_converged(self):
        res = solve_scalar(F_CIRCLE, 4.0, 1.0e6, max_iter=3)
        self.assertFalse(res.converged)
        with self.assertRaises(NotConvergedError) as ctx:
            implicit_derivative(F_CIRCLE, 4.0, 1.0e6, max_iter=3)
        self.assertFalse(ctx.exception.result.converged)

    def test_cubic_implicit(self):
        # y^3 + y = x  =>  dy/dx = 1 / (3 y^2 + 1)
        F = lambda x, y: y ** 3 + y - x
        x = 10.0
        res = solve_scalar(F, x, 2.0)
        self.assertTrue(res.converged)
        y = res.root
        self.assertAlmostEqual(y ** 3 + y, x, places=9)
        self.assertAlmostEqual(implicit_derivative(F, x, 2.0),
                               1.0 / (3 * y * y + 1), places=9)


class TestSystemImplicit(unittest.TestCase):
    @staticmethod
    def F(x, y):
        return [y[0] ** 2 + y[1] ** 2 - x[0], y[0] - y[1] - 1.0]

    def test_two_equation_system(self):
        res = solve_system(self.F, [10.0], [3.0, 2.0])
        self.assertTrue(res.converged)
        self.assertFalse(res.singular)
        y0, y1 = res.root
        self.assertAlmostEqual(y0 ** 2 + y1 ** 2, 10.0, places=9)
        self.assertAlmostEqual(y0 - y1, 1.0, places=9)
        # analytic: dy0/dx = dy1/dx = 1 / (2 (y0 + y1))
        D = implicit_jacobian(self.F, [10.0], [3.0, 2.0])
        want = 1.0 / (2.0 * (y0 + y1))
        self.assertAlmostEqual(D[0][0], want, places=8)
        self.assertAlmostEqual(D[1][0], want, places=8)

    def test_system_derivative_matches_fd(self):
        h = 1e-6
        D = implicit_jacobian(self.F, [10.0], [3.0, 2.0])
        up = solve_system(self.F, [10.0 + h], [3.0, 2.0])
        down = solve_system(self.F, [10.0 - h], [3.0, 2.0])
        for i in range(2):
            fd = (up.root[i] - down.root[i]) / (2 * h)
            self.assertAlmostEqual(D[i][0], fd, places=5)

    def test_singular_system(self):
        def F(x, y):
            return [y[0] + y[1] - x[0], 2 * y[0] + 2 * y[1] - 2 * x[0]]
        res = solve_system(F, [1.0], [0.5, 0.5])
        self.assertTrue(res.singular)
        with self.assertRaises(SingularJacobianError):
            implicit_jacobian(F, [1.0], [0.5, 0.5])

    def test_system_not_converged(self):
        res = solve_system(self.F, [10.0], [1.0e8, 1.0e8], max_iter=2)
        self.assertFalse(res.converged)
        with self.assertRaises(NotConvergedError):
            implicit_jacobian(self.F, [10.0], [1.0e8, 1.0e8], max_iter=2)


if __name__ == "__main__":
    unittest.main()
