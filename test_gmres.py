"""gmres 的 unittest 测试套件（仅标准库）。运行：python3.11 -m unittest -v"""

from __future__ import annotations

import math
import random
import unittest

from dense import DenseMatrix, dense_solve
from gmres import MatvecError, gmres


def poisson_matrix(n: int) -> DenseMatrix:
    """一维 Poisson 三对角矩阵 [-1, 2, -1]。"""
    rows = [[0.0] * n for _ in range(n)]
    for i in range(n):
        rows[i][i] = 2.0
        if i > 0:
            rows[i][i - 1] = -1.0
        if i + 1 < n:
            rows[i][i + 1] = -1.0
    return DenseMatrix(rows)


def random_diagonally_dominant(n: int, seed: int) -> DenseMatrix:
    rng = random.Random(seed)
    rows = [[rng.uniform(-1.0, 1.0) for _ in range(n)] for _ in range(n)]
    for i in range(n):
        rows[i][i] += 2.0 * n  # 严格对角占优，保证非奇异
    return DenseMatrix(rows)


def diagonal_matrix(values) -> DenseMatrix:
    n = len(values)
    rows = [[0.0] * n for _ in range(n)]
    for i, v in enumerate(values):
        rows[i][i] = float(v)
    return DenseMatrix(rows)


class TestZeroRhs(unittest.TestCase):
    def test_zero_rhs_returns_immediately(self):
        def forbidden_matvec(x, n):
            raise AssertionError("零右端不应调用 matvec")

        res = gmres(forbidden_matvec, [0.0, 0.0, 0.0], 3)
        self.assertTrue(res.converged)
        self.assertEqual(res.reason, "zero_rhs")
        self.assertEqual(res.x, [0.0, 0.0, 0.0])
        self.assertEqual(res.matvec_count, 0)
        self.assertEqual(res.restart_cycles, 0)
        self.assertEqual(res.residual_norm, 0.0)

    def test_zero_rhs_returns_x0_copy(self):
        def forbidden_matvec(x, n):
            raise AssertionError("零右端不应调用 matvec")

        x0 = [1.0, 2.0]
        res = gmres(forbidden_matvec, [0.0, 0.0], 2, x0=x0)
        self.assertEqual(res.x, x0)
        self.assertIsNot(res.x, x0)
        self.assertEqual(res.matvec_count, 0)


class TestConvergence(unittest.TestCase):
    def test_identity_converges_in_one_step(self):
        n = 6
        a = DenseMatrix([[1.0 if i == j else 0.0 for j in range(n)]
                         for i in range(n)])
        b = [float(i + 1) for i in range(n)]
        res = gmres(a.matvec, b, n, tol=1e-12)
        self.assertTrue(res.converged)
        self.assertEqual(res.reason, "converged")
        self.assertEqual(res.arnoldi_steps, 1)
        for xi, bi in zip(res.x, b):
            self.assertAlmostEqual(xi, bi, places=12)

    def test_poisson_matches_dense(self):
        n = 12
        a = poisson_matrix(n)
        rng = random.Random(7)
        b = [rng.uniform(-1.0, 1.0) for _ in range(n)]
        res = gmres(a.matvec, b, n, restart=n, tol=1e-12)
        self.assertTrue(res.converged)
        x_ref = dense_solve(a.rows, b)
        err = max(abs(xi - ri) for xi, ri in zip(res.x, x_ref))
        self.assertLess(err, 1e-8)

    def test_nonsymmetric_matches_dense(self):
        n = 10
        a = random_diagonally_dominant(n, seed=42)
        rng = random.Random(1)
        b = [rng.uniform(-5.0, 5.0) for _ in range(n)]
        res = gmres(a.matvec, b, n, restart=n, tol=1e-13)
        self.assertTrue(res.converged)
        x_ref = dense_solve(a.rows, b)
        err = max(abs(xi - ri) for xi, ri in zip(res.x, x_ref))
        self.assertLess(err, 1e-7)

    def test_restart_cycles_and_history(self):
        n = 30
        rng = random.Random(3)
        a = diagonal_matrix([rng.uniform(1.0, 10.0) for _ in range(n)])
        b = [rng.uniform(-1.0, 1.0) for _ in range(n)]
        res = gmres(a.matvec, b, n, restart=5, tol=1e-10, max_restarts=200)
        self.assertTrue(res.converged)
        self.assertGreater(res.restart_cycles, 1)  # 确实发生了重启
        self.assertEqual(len(res.residual_history), res.arnoldi_steps)
        self.assertEqual(len(res.restart_residuals), res.restart_cycles + 1)
        self.assertAlmostEqual(res.restart_residuals[0], math.hypot(*b))
        self.assertAlmostEqual(res.residual_norm, res.restart_residuals[-1])
        # 重启边界的真实残差应单调不增（允许微小舍入）。
        for prev, cur in zip(res.restart_residuals, res.restart_residuals[1:]):
            self.assertLessEqual(cur, prev * (1.0 + 1e-9))
        self.assertLessEqual(res.residual_norm, 1e-10 * math.hypot(*b))

    def test_exact_x0_converges_without_arnoldi(self):
        n = 8
        a = poisson_matrix(n)
        x_true = [float(i) - 3.0 for i in range(n)]
        b = a.matvec(x_true, n)
        res = gmres(a.matvec, b, n, x0=x_true, tol=1e-12)
        self.assertTrue(res.converged)
        self.assertEqual(res.arnoldi_steps, 0)
        self.assertEqual(res.restart_cycles, 0)
        self.assertEqual(res.matvec_count, 1)  # 仅初始残差一次


class TestBudget(unittest.TestCase):
    def test_max_matvec_budget_not_converged(self):
        n = 20
        a = random_diagonally_dominant(n, seed=5)
        b = [1.0] * n
        res = gmres(a.matvec, b, n, restart=10, max_matvec=3)
        self.assertFalse(res.converged)
        self.assertEqual(res.reason, "budget_exhausted")
        self.assertLessEqual(res.matvec_count, 3)

    def test_max_restarts_budget_not_converged(self):
        n = 30
        a = random_diagonally_dominant(n, seed=6)
        b = [1.0] * n
        res = gmres(a.matvec, b, n, restart=2, max_restarts=1, tol=1e-14)
        self.assertFalse(res.converged)
        self.assertEqual(res.reason, "budget_exhausted")
        self.assertEqual(res.restart_cycles, 1)

    def test_stagnation_on_zero_matrix(self):
        n = 5
        a = DenseMatrix([[0.0] * n for _ in range(n)])
        b = [1.0] * n
        res = gmres(a.matvec, b, n, restart=4)
        self.assertFalse(res.converged)
        self.assertEqual(res.reason, "stagnation")
        self.assertEqual(res.restart_cycles, 1)


class TestCallbackValidation(unittest.TestCase):
    def setUp(self):
        self.n = 4
        self.b = [1.0, 2.0, 3.0, 4.0]

    def test_wrong_size_callback_located(self):
        def bad_matvec(x, n):
            return [0.0] * (n + 1)

        with self.assertRaises(MatvecError) as ctx:
            gmres(bad_matvec, self.b, self.n)
        msg = str(ctx.exception)
        self.assertIn("重启周期 1", msg)
        self.assertIn("长度", msg)

    def test_nonfinite_callback_located(self):
        def nan_matvec(x, n):
            y = [0.0] * n
            y[2] = float("nan")
            return y

        with self.assertRaises(MatvecError) as ctx:
            gmres(nan_matvec, self.b, self.n)
        msg = str(ctx.exception)
        self.assertIn("第 2 个分量", msg)
        self.assertIn("重启周期 1", msg)

    def test_callback_exception_wrapped(self):
        def boom(x, n):
            raise RuntimeError("boom")

        with self.assertRaises(MatvecError) as ctx:
            gmres(boom, self.b, self.n)
        self.assertIn("boom", str(ctx.exception))
        self.assertIsInstance(ctx.exception.__cause__, RuntimeError)

    def test_nonfinite_input_b(self):
        a = poisson_matrix(self.n)
        b = [1.0, float("inf"), 3.0, 4.0]
        with self.assertRaises(ValueError) as ctx:
            gmres(a.matvec, b, self.n)
        self.assertIn("b[1]", str(ctx.exception))

    def test_nonfinite_input_x0(self):
        a = poisson_matrix(self.n)
        with self.assertRaises(ValueError) as ctx:
            gmres(a.matvec, self.b, self.n, x0=[0.0, float("nan"), 0.0, 0.0])
        self.assertIn("x0[1]", str(ctx.exception))

    def test_size_mismatch_explicit_n(self):
        a = poisson_matrix(3)
        with self.assertRaises(ValueError):
            gmres(a.matvec, [1.0, 2.0], 3)
        with self.assertRaises(ValueError):
            gmres(a.matvec, [1.0, 2.0, 3.0], 3, x0=[0.0, 0.0])


class TestDenseHelpers(unittest.TestCase):
    def test_dense_solve_known_answer(self):
        a = DenseMatrix([[3.0, 1.0], [1.0, 2.0]])
        x = dense_solve(a.rows, [9.0, 8.0])
        self.assertAlmostEqual(x[0], 2.0)
        self.assertAlmostEqual(x[1], 3.0)

    def test_dense_solve_singular(self):
        with self.assertRaises(ValueError):
            dense_solve([[1.0, 2.0], [2.0, 4.0]], [1.0, 2.0])

    def test_dense_matrix_validates_shape(self):
        with self.assertRaises(ValueError):
            DenseMatrix([[1.0, 2.0], [3.0]])
        with self.assertRaises(ValueError):
            DenseMatrix([[float("nan")]])


if __name__ == "__main__":
    unittest.main()
