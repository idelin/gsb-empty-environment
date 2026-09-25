"""Unit tests for gmres.py (standard library + unittest only)."""

import math
import random
import unittest

from gmres import (
    GmresError,
    MatvecShapeError,
    NonFiniteError,
    gmres,
)


def dense_solve(A, b):
    """Self-written dense Gaussian elimination with partial pivoting."""
    n = len(A)
    M = [list(A[i]) + [b[i]] for i in range(n)]
    for col in range(n):
        piv = max(range(col, n), key=lambda r: abs(M[r][col]))
        if M[piv][col] == 0.0:
            raise ValueError("singular matrix")
        M[col], M[piv] = M[piv], M[col]
        for r in range(col + 1, n):
            f = M[r][col] / M[col][col]
            if f != 0.0:
                for c in range(col, n + 1):
                    M[r][c] -= f * M[col][c]
    x = [0.0] * n
    for i in range(n - 1, -1, -1):
        s = math.fsum(M[i][j] * x[j] for j in range(i + 1, n))
        x[i] = (M[i][n] - s) / M[i][i]
    return x


def make_dense_matvec(A):
    def matvec(x):
        return [math.fsum(aij * xj for aij, xj in zip(row, x)) for row in A]
    return matvec


def random_dominant_matrix(n, seed):
    """Diagonally dominant (hence nonsingular) random matrix."""
    rng = random.Random(seed)
    A = [[rng.uniform(-1.0, 1.0) for _ in range(n)] for _ in range(n)]
    for i in range(n):
        A[i][i] += n + 1.0
    return A


class TestGmres(unittest.TestCase):
    def test_zero_rhs_returns_immediately(self):
        def matvec(x):
            raise AssertionError("matvec must not be called for zero rhs")

        x0 = [1.0, -2.0, 3.0]
        res = gmres(matvec, [0.0, 0.0, 0.0], n=3, x0=x0)
        self.assertTrue(res.converged)
        self.assertEqual(res.reason, "zero_rhs")
        self.assertEqual(res.x, x0)
        self.assertEqual(res.iterations, 0)
        self.assertEqual(res.matvec_calls, 0)
        self.assertEqual(res.residual_history, [])

    def test_matches_dense_solve_small(self):
        n = 8
        A = random_dominant_matrix(n, seed=42)
        rng = random.Random(7)
        b = [rng.uniform(-5.0, 5.0) for _ in range(n)]
        res = gmres(make_dense_matvec(A), b, n=n, restart=3,
                    tol=1e-12, maxiter=200)
        self.assertTrue(res.converged, msg=res.reason)
        x_ref = dense_solve(A, b)
        err = max(abs(a - b_) for a, b_ in zip(res.x, x_ref))
        self.assertLess(err, 1e-6)
        bnorm = math.sqrt(math.fsum(t * t for t in b))
        self.assertLessEqual(res.true_residual, 1e-12 * bnorm * (1 + 1e-9))

    def test_residual_history_recorded_and_monotone(self):
        n = 10
        A = random_dominant_matrix(n, seed=1)
        b = [1.0] * n
        res = gmres(make_dense_matvec(A), b, n=n, restart=4, tol=1e-10)
        self.assertTrue(res.converged, msg=res.reason)
        self.assertEqual(len(res.residual_history), res.iterations)
        for prev, cur in zip(res.residual_history, res.residual_history[1:]):
            self.assertLessEqual(cur, prev * (1 + 1e-9) + 1e-300)

    def test_restart_cycles_and_memory_bound(self):
        n = 12
        A = random_dominant_matrix(n, seed=2)
        b = [float(i + 1) for i in range(n)]
        restart = 4
        res = gmres(make_dense_matvec(A), b, n=n, restart=restart,
                    tol=1e-10, maxiter=100)
        self.assertTrue(res.converged, msg=res.reason)
        self.assertGreaterEqual(res.restart_cycles, 1)
        self.assertEqual(len(res.cycle_residuals), res.restart_cycles)
        # working memory: never more than restart + 1 live basis vectors
        self.assertLessEqual(res.peak_krylov_vectors, restart + 1)

    def test_budget_exhausted_is_not_converged(self):
        n = 20
        A = random_dominant_matrix(n, seed=3)
        b = [1.0] * n
        res = gmres(make_dense_matvec(A), b, n=n, restart=10, maxiter=2)
        self.assertFalse(res.converged)
        self.assertEqual(res.reason, "budget_exhausted")
        self.assertEqual(res.iterations, 2)
        self.assertEqual(len(res.residual_history), 2)
        self.assertIsNotNone(res.true_residual)

    def test_stagnation_zero_operator(self):
        res = gmres(lambda x: [0.0] * 4, [1.0, 2.0, 3.0, 4.0], n=4)
        self.assertFalse(res.converged)
        self.assertEqual(res.reason, "stagnation")

    def test_stagnation_slow_progress(self):
        # eigenvalues 1..100, GMRES(1) improves ~2% per cycle, below the
        # 5% stagnation threshold -> cycle-based stagnation triggers
        n = 100

        def matvec(x):
            return [(i + 1) * x[i] for i in range(n)]

        res = gmres(matvec, [1.0] * n, n=n, restart=1,
                    stagnation_rtol=0.05, maxiter=1000)
        self.assertFalse(res.converged)
        self.assertEqual(res.reason, "stagnation")

    def test_happy_breakdown_exact_solution(self):
        n = 5
        diag = [1.0, 2.0, 3.0, 4.0, 5.0]
        # b lives in span(e1, e2): the Krylov subspace is 2-dimensional,
        # so Arnoldi breaks down exactly (h = 0) at step 2.
        b = [1.0, -1.0, 0.0, 0.0, 0.0]
        res = gmres(lambda x: [d * xi for d, xi in zip(diag, x)],
                    b, n=n, restart=8, tol=1e-14)
        self.assertTrue(res.converged)
        self.assertEqual(res.reason, "happy_breakdown")
        for xi, (d, bi) in zip(res.x, zip(diag, b)):
            self.assertAlmostEqual(xi, bi / d, places=12)

    def test_callback_receives_vector_of_length_n(self):
        n = 6
        seen = []

        def matvec(x):
            seen.append(len(x))
            return [2.0 * xi for xi in x]

        res = gmres(matvec, [1.0] * n, n=n, restart=3, tol=1e-10)
        self.assertTrue(res.converged)
        self.assertTrue(seen)
        self.assertTrue(all(length == n for length in seen))

    def test_matvec_wrong_length_located(self):
        def bad_matvec(x):
            return [0.0] * 5  # expected 4

        with self.assertRaises(MatvecShapeError) as ctx:
            gmres(bad_matvec, [1.0, 2.0, 3.0, 4.0], n=4)
        msg = str(ctx.exception)
        self.assertIn("length 5", msg)
        self.assertIn("expected n=4", msg)
        self.assertIn("restart cycle 0", msg)

    def test_matvec_nonfinite_located(self):
        calls = []

        def nan_matvec(x):
            calls.append(1)
            if len(calls) == 1:
                return [0.0, 0.0, 0.0, 0.0]  # residual evaluation is fine
            return [1.0, float("nan"), 0.0, 0.0]  # first Arnoldi step

        with self.assertRaises(NonFiniteError) as ctx:
            gmres(nan_matvec, [1.0, 0.0, 0.0, 1.0], n=4)
        msg = str(ctx.exception)
        self.assertIn("y[1]", msg)
        self.assertIn("restart cycle 0, Arnoldi step 0", msg)

    def test_b_nonfinite_located(self):
        with self.assertRaises(NonFiniteError) as ctx:
            gmres(lambda x: x, [1.0, float("inf"), 2.0], n=3)
        self.assertIn("b[1]", str(ctx.exception))

    def test_x0_wrong_length(self):
        with self.assertRaises(MatvecShapeError) as ctx:
            gmres(lambda x: x, [1.0, 2.0], n=2, x0=[1.0, 2.0, 3.0])
        self.assertIn("x0 has length 3, expected n=2", str(ctx.exception))

    def test_b_wrong_length(self):
        with self.assertRaises(MatvecShapeError):
            gmres(lambda x: x, [1.0, 2.0, 3.0], n=2)

    def test_invalid_n_rejected(self):
        with self.assertRaises(GmresError):
            gmres(lambda x: x, [1.0], n=0)

    def test_x0_warm_start(self):
        n = 6
        A = random_dominant_matrix(n, seed=9)
        x_true = [float(i) for i in range(n)]
        mv = make_dense_matvec(A)
        b = mv(x_true)
        res = gmres(mv, b, n=n, x0=list(x_true), tol=1e-12)
        self.assertTrue(res.converged)
        self.assertEqual(res.iterations, 0)  # x0 already solves it


if __name__ == "__main__":
    unittest.main()
