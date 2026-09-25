"""Unit tests for minigrad: HVP correctness, zero second derivative of
constant gradients, shared-structure handling, and residue-free repeats.

Numerical verification is independent: finite differences are computed
with plain floats + math.exp, never touching the Tensor engine.
"""

import math
import unittest

from minigrad import Tensor, grad, hvp


def f_quadratic(x, a, b):
    """f(x) = 0.5 * x^T A x + b^T x, with x, b column vectors."""
    return (x.T @ a @ x).sum() * 0.5 + (b * x).sum()


A_DATA = [2.0, 0.5, -1.0,
          0.5, 3.0, 0.25,
          -1.0, 0.25, 1.5]  # symmetric 3x3
B_DATA = [1.0, -2.0, 0.5]
X0 = [0.5, -1.0, 2.0]
V = [1.5, 0.5, -1.0]


def matvec(a_flat, v, n=3):
    return [sum(a_flat[i * n + j] * v[j] for j in range(n)) for i in range(n)]


class TestQuadraticHVP(unittest.TestCase):
    def test_hvp_matches_analytic(self):
        a = Tensor(A_DATA, (3, 3))
        b = Tensor(B_DATA, (3, 1))
        x = Tensor(X0, (3, 1), requires_grad=True)
        v = Tensor(V, (3, 1))
        hv = hvp(f_quadratic(x, a, b), x, v)
        expected = matvec(A_DATA, V)  # Hessian of quadratic is A (symmetric)
        for got, want in zip(hv.data, expected):
            self.assertAlmostEqual(got, want, places=12)

    def test_gradient_matches_analytic(self):
        a = Tensor(A_DATA, (3, 3))
        b = Tensor(B_DATA, (3, 1))
        x = Tensor(X0, (3, 1), requires_grad=True)
        g = grad(f_quadratic(x, a, b), x)
        expected = [u + w for u, w in zip(matvec(A_DATA, X0), B_DATA)]
        for got, want in zip(g.data, expected):
            self.assertAlmostEqual(got, want, places=12)


class TestConstantGradient(unittest.TestCase):
    def test_second_derivative_is_zero(self):
        c = Tensor([1.5, -0.5, 2.0])
        x = Tensor(X0, requires_grad=True)
        f = (c * x).sum() + 3.0  # linear: gradient constant, Hessian = 0
        hv = hvp(f, x, Tensor(V))
        self.assertEqual(hv.shape, (3,))
        for got in hv.data:
            self.assertAlmostEqual(got, 0.0, places=15)

    def test_second_grad_of_scalar_is_zero(self):
        x = Tensor(2.0, requires_grad=True)
        f = 4.0 * x - 1.0
        g1 = grad(f, x, create_graph=True)
        g2 = grad(g1.sum(), x)
        self.assertAlmostEqual(g1.item(), 4.0, places=15)
        self.assertAlmostEqual(g2.item(), 0.0, places=15)


class TestSharedStructure(unittest.TestCase):
    def test_shared_subgraph_first_and_second_order(self):
        # y used twice: z = y*y + y with y = x*x
        # dz/dx = 4x^3 + 2x, d2z/dx2 = 12x^2 + 2
        x = Tensor(3.0, requires_grad=True)
        y = x * x
        z = y * y + y
        g1 = grad(z, x, create_graph=True)
        self.assertAlmostEqual(g1.item(), 114.0, places=12)
        g2 = grad(g1.sum(), x)
        self.assertAlmostEqual(g2.item(), 110.0, places=12)

    def test_hvp_shared_subgraph(self):
        # f = (sum(x))^2 via shared s: Hessian is all-ones * 2
        x = Tensor([1.0, -2.0, 0.5], requires_grad=True)
        s = x.sum()
        f = s * s
        hv = hvp(f, x, Tensor(V))
        for got in hv.data:
            self.assertAlmostEqual(got, 2.0 * sum(V), places=12)


class TestNoResidueNoPollution(unittest.TestCase):
    def _build(self):
        w = Tensor([0.5, -1.0, 2.0])
        x = Tensor([0.3, -0.7, 1.2], requires_grad=True)
        f = (w * x).sum().exp() + (x * x * x).sum()
        return x, f

    def test_repeated_grad_does_not_accumulate(self):
        x, f = self._build()
        g1 = grad(f, x)
        snapshot = list(g1.data)
        g2 = grad(f, x)
        g3 = grad(f, x)
        self.assertIsNot(g1, g2)
        self.assertEqual(g1.data, snapshot)  # old buffer untouched
        self.assertEqual(g2.data, snapshot)
        self.assertEqual(g3.data, snapshot)
        self.assertEqual(x.grad.data, snapshot)

    def test_repeated_hvp_does_not_accumulate(self):
        x, f = self._build()
        v = Tensor(V)
        h1 = hvp(f, x, v)
        h2 = hvp(f, x, v)
        self.assertEqual(h1.data, h2.data)

    def test_input_data_not_polluted(self):
        x, f = self._build()
        before = list(x.data)
        grad(f, x, create_graph=True)
        hvp(f, x, Tensor(V))
        grad(f, x)
        self.assertEqual(x.data, before)

    def test_plain_backward_returns_plain_tensor(self):
        x, f = self._build()
        g = grad(f, x)  # create_graph=False: no graph attached
        self.assertFalse(g.requires_grad)
        self.assertEqual(g._prev, ())


class TestIndependentNumericalVerification(unittest.TestCase):
    """Finite-difference check written in pure floats (no Tensor engine)."""

    XS = [0.3, -0.7, 1.2]
    W = [0.5, -1.0, 2.0]

    @staticmethod
    def f_plain(xs):
        return math.exp(sum(wi * xi for wi, xi in zip(TestIndependentNumericalVerification.W, xs))) \
            + sum(xi ** 3 for xi in xs)

    @classmethod
    def grad_plain(cls, xs, h=1e-5):
        out = []
        for i in range(len(xs)):
            xp, xm = list(xs), list(xs)
            xp[i] += h
            xm[i] -= h
            out.append((cls.f_plain(xp) - cls.f_plain(xm)) / (2.0 * h))
        return out

    @classmethod
    def hvp_plain(cls, xs, v, h=1e-3):
        xp = [x + h * vi for x, vi in zip(xs, v)]
        xm = [x - h * vi for x, vi in zip(xs, v)]
        gp, gm = cls.grad_plain(xp), cls.grad_plain(xm)
        return [(a - b) / (2.0 * h) for a, b in zip(gp, gm)]

    def test_hvp_matches_finite_differences(self):
        w = Tensor(self.W)
        x = Tensor(self.XS, requires_grad=True)
        f = (w * x).sum().exp() + (x * x * x).sum()
        hv = hvp(f, x, Tensor(V))
        expected = self.hvp_plain(self.XS, V)
        for got, want in zip(hv.data, expected):
            self.assertAlmostEqual(got, want, delta=1e-4)

    def test_grad_matches_finite_differences(self):
        w = Tensor(self.W)
        x = Tensor(self.XS, requires_grad=True)
        f = (w * x).sum().exp() + (x * x * x).sum()
        g = grad(f, x)
        expected = self.grad_plain(self.XS)
        for got, want in zip(g.data, expected):
            self.assertAlmostEqual(got, want, delta=1e-6)


if __name__ == "__main__":
    unittest.main(verbosity=2)
