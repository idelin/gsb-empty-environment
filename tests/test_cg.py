"""Runnable tests for the sparse CG solver (standard-library unittest only)."""

import json
import math
import random
import unittest

from sparse_cg import (
    CGSolver,
    DiagonalPreconditioner,
    MatrixVersionError,
    SparseMatrix,
    ZeroDiagonalError,
    STATUS_ASYMMETRIC,
    STATUS_CANCELLED,
    STATUS_CONVERGED,
    STATUS_INDEFINITE,
    STATUS_INDEFINITE_PRECONDITIONER,
    STATUS_MAX_ITER,
    STATUS_STAGNATED,
)
from sparse_cg.dense import dense_from_sparse, solve_dense


def poisson_1d(n, version=0):
    """Classic SPD tridiagonal (2 on the diagonal, -1 off it)."""
    rows, cols, vals = [], [], []
    for i in range(n):
        rows.append(i); cols.append(i); vals.append(2.0)
        if i > 0:
            rows.append(i); cols.append(i - 1); vals.append(-1.0)
        if i < n - 1:
            rows.append(i); cols.append(i + 1); vals.append(-1.0)
    return SparseMatrix(n, n, rows, cols, vals, version=version)


def diagonal_matrix(diag, version=0):
    n = len(diag)
    return SparseMatrix(n, n, list(range(n)), list(range(n)), list(diag),
                        version=version)


def ill_conditioned_spd(version=0):
    """Diagonal SPD: eigenvalue cluster in [1, 10] plus outliers up to 1e8.

    In finite precision the true residual stagnates around 1e-13 * ||b||
    while the recursive residual keeps decreasing far below it, which is
    exactly the divergence this solver must survive.
    """
    diag = [1.0 + 9.0 * i / 94 for i in range(95)] + [1e4, 1e5, 1e6, 1e7, 1e8]
    return diagonal_matrix(diag, version=version)


def norm2(v):
    return math.sqrt(math.fsum(x * x for x in v))


class FlakyMatrix:
    """Matrix wrapper whose matvec raises after a fixed number of calls."""

    def __init__(self, inner, fail_at_call):
        self._inner = inner
        self._fail_at = fail_at_call
        self.calls = 0
        self.n_rows = inner.n_rows
        self.n_cols = inner.n_cols
        self.version = inner.version

    def matvec(self, x):
        self.calls += 1
        if self.calls == self._fail_at:
            raise RuntimeError("simulated matvec failure")
        return self._inner.matvec(x)


class TestExactInitialGuess(unittest.TestCase):
    def test_exact_x0_returns_in_zero_iterations(self):
        n = 10
        A = poisson_1d(n)
        x_exact = [math.sin(0.3 * i) + 0.1 * i for i in range(n)]
        b = A.matvec(x_exact)
        result = CGSolver(A, rtol=1e-12, atol=1e-14).solve(b, x0=x_exact)
        self.assertEqual(result.status, STATUS_CONVERGED)
        self.assertEqual(result.iterations, 0)
        self.assertLessEqual(result.true_residual_norm, result.tolerance)


class TestIllConditionedTrueResidual(unittest.TestCase):
    """Recursive and true residuals diverge; only the true one may terminate."""

    def test_termination_uses_true_residual(self):
        n = 100
        A = ill_conditioned_spd()
        rng = random.Random(2024)
        b = [rng.uniform(-1.0, 1.0) for _ in range(n)]
        # Tolerance below the reachable true-residual floor (~1e-13 * ||b||)
        # but above where the recursive residual ends up (< 1e-15 * ||b||).
        solver = CGSolver(A, rtol=1e-15, atol=0.0, true_residual_period=10,
                          stagnation_window=6, stagnation_factor=0.98)
        result = solver.solve(b, max_iter=600)

        # The true residual cannot reach the tolerance: the run must end in
        # stagnation (or max_iter), never in a false "converged".
        self.assertIn(result.status, (STATUS_STAGNATED, STATUS_MAX_ITER))
        self.assertFalse(result.converged)
        self.assertGreater(result.true_residual_norm, result.tolerance)

        # Divergence evidence: at some iteration the recursive residual was
        # below tolerance while the recomputed true residual was not.
        diverged = [h for h in result.history
                    if h[1] is not None and h[2] <= result.tolerance < h[1]]
        self.assertTrue(
            diverged,
            "expected recursive residual below tolerance while true residual "
            "stayed above it (no drift observed)",
        )
        # The gap is substantial, not a rounding artefact.
        self.assertGreater(max(h[1] / h[2] for h in diverged), 10.0)
        # And the solver did NOT stop at the first such point: it kept going
        # until a true-residual-based decision (stagnation here).
        self.assertGreater(result.iterations, diverged[0][0])
        if result.status == STATUS_STAGNATED:
            self.assertIn("true_residual_checkpoints", result.evidence)


class TestCheckpointAndVersions(unittest.TestCase):
    def _cancelled_solver(self):
        n = 40
        A = poisson_1d(n, version=1)
        rng = random.Random(11)
        b = [rng.uniform(-1.0, 1.0) for _ in range(n)]
        solver = CGSolver(A, rtol=1e-10, atol=0.0)
        result = solver.solve(b, max_iter=500,
                              iteration_callback=lambda k, res: k < 5)
        self.assertEqual(result.status, STATUS_CANCELLED)
        self.assertFalse(result.converged)
        return A, b, solver, result

    def test_cancel_then_checkpoint_resume_converges(self):
        A, b, solver, cancelled = self._cancelled_solver()
        ckpt = solver.checkpoint()
        json.dumps(ckpt)  # must stay JSON-serialisable
        self.assertEqual(ckpt["matrix_version"], 1)

        restored = CGSolver.restore(ckpt, A)
        result = restored.resume(max_iter=500)
        self.assertEqual(result.status, STATUS_CONVERGED)
        self.assertLessEqual(result.true_residual_norm, result.tolerance)
        self.assertGreater(result.iterations, cancelled.iterations)

        x_ref = solve_dense(dense_from_sparse(A), b)
        err = max(abs(a - c) for a, c in zip(result.x, x_ref))
        self.assertLess(err, 1e-6)

    def test_matrix_version_change_rejects_stale_search_directions(self):
        A, b, solver, _ = self._cancelled_solver()
        ckpt = solver.checkpoint()
        A_changed = poisson_1d(40, version=2)  # same values, new version
        with self.assertRaises(MatrixVersionError) as ctx:
            CGSolver.restore(ckpt, A_changed)
        self.assertEqual(ctx.exception.expected, 1)
        self.assertEqual(ctx.exception.found, 2)


class TestWarmStartReinitializesKrylov(unittest.TestCase):
    def test_warm_start_after_matrix_version_change(self):
        n = 100
        A1 = poisson_1d(n, version=1)
        rng = random.Random(5)
        b = [rng.uniform(-1.0, 1.0) for _ in range(n)]
        r1 = CGSolver(A1, rtol=1e-12, atol=0.0).solve(b, max_iter=2000)
        self.assertEqual(r1.status, STATUS_CONVERGED)

        # Matrix changes (small perturbation, new version): reuse the old
        # solution as x0. The Krylov state must be rebuilt from scratch.
        rows, cols, vals = [], [], []
        for i, j, v in A1.entries():
            vals.append(v * (1.0 + 1e-4 if i == j else 1.0))
            rows.append(i); cols.append(j)
        A2 = SparseMatrix(n, n, rows, cols, vals, version=2)
        solver2 = CGSolver(A2, rtol=1e-12, atol=0.0)
        r2 = solver2.solve(b, x0=r1.x, max_iter=2000)
        self.assertEqual(r2.status, STATUS_CONVERGED)

        # Proof of reinitialisation: the first recorded residual equals the
        # true residual of the warm-started x0 under the NEW matrix.
        expected_r0 = norm2([bi - ai for bi, ai in zip(b, A2.matvec(r1.x))])
        self.assertAlmostEqual(r2.history[0][1], expected_r0, places=12)
        self.assertAlmostEqual(r2.initial_residual_norm, expected_r0, places=12)
        # The warm start begins orders of magnitude ahead of a cold start
        # on the same matrix, and never does more work. (For this matrix CG
        # exhibits finite termination at k == n, so the iteration count
        # cannot be strictly smaller; the initial residual is the honest
        # measure of the warm-start benefit.)
        cold = CGSolver(A2, rtol=1e-12, atol=0.0).solve(b, max_iter=2000)
        self.assertEqual(cold.status, STATUS_CONVERGED)
        self.assertLess(r2.initial_residual_norm,
                        1e-2 * cold.initial_residual_norm)
        self.assertLessEqual(r2.iterations, cold.iterations)


class TestPreconditioner(unittest.TestCase):
    def test_diagonal_preconditioner_update_and_exactness(self):
        A = diagonal_matrix([1.0, 10.0, 100.0, 1000.0], version=1)
        pre = DiagonalPreconditioner(4).update_from_matrix(A)
        self.assertEqual(pre.diag, [1.0, 10.0, 100.0, 1000.0])
        b = [1.0, 2.0, 3.0, 4.0]
        # M = A exactly, so preconditioned CG converges in one iteration.
        result = CGSolver(A, preconditioner=pre, rtol=1e-14, atol=0.0).solve(b)
        self.assertEqual(result.status, STATUS_CONVERGED)
        self.assertEqual(result.iterations, 1)

        # The preconditioner is updatable after the matrix changes.
        A2 = diagonal_matrix([2.0, 20.0, 200.0, 2000.0], version=2)
        pre.update_from_matrix(A2)
        self.assertEqual(pre.diag, [2.0, 20.0, 200.0, 2000.0])

    def test_zero_diagonal_entries(self):
        A = SparseMatrix(3, 3, [0, 1, 2, 0], [0, 1, 2, 1], [4.0, 0.0, 9.0, 1.0])
        pre = DiagonalPreconditioner(3)
        with self.assertRaises(ZeroDiagonalError) as ctx:
            pre.update_from_matrix(A, zero_policy="raise")
        self.assertEqual(ctx.exception.indices, [1])
        pre.update_from_matrix(A, zero_policy="unit")
        self.assertEqual(pre.zero_replacements, 1)
        self.assertEqual(pre.diag[1], 1.0)

    def test_indefinite_preconditioner_detected_with_evidence(self):
        A = poisson_1d(6)
        pre = DiagonalPreconditioner(6)
        pre.diag = [-1.0] * 6  # not positive definite
        b = [1.0] * 6
        result = CGSolver(A, preconditioner=pre).solve(b)
        self.assertEqual(result.status, STATUS_INDEFINITE_PRECONDITIONER)
        self.assertFalse(result.converged)
        self.assertLessEqual(result.evidence["rz"], 0.0)


class TestFailureAndRestart(unittest.TestCase):
    def test_failing_matvec_then_restart_from_partial_x(self):
        n = 30
        A = poisson_1d(n, version=1)
        rng = random.Random(3)
        b = [rng.uniform(-1.0, 1.0) for _ in range(n)]
        x_ref = solve_dense(dense_from_sparse(A), b)

        flaky = FlakyMatrix(A, fail_at_call=3)
        solver = CGSolver(flaky, rtol=1e-10, atol=0.0)
        with self.assertRaises(RuntimeError):
            solver.solve(b, max_iter=200)
        x_partial = solver.current_x
        self.assertIsNotNone(x_partial)

        # Restart with a healthy matrix; the partial solution is reused as a
        # warm start and the Krylov state is reinitialised.
        solver2 = CGSolver(A, rtol=1e-10, atol=0.0)
        result = solver2.solve(b, x0=x_partial, max_iter=200)
        self.assertEqual(result.status, STATUS_CONVERGED)
        err = max(abs(a - c) for a, c in zip(result.x, x_ref))
        self.assertLess(err, 1e-6)


class TestDenseComparison(unittest.TestCase):
    def test_matches_exact_elimination_on_small_spd(self):
        rng = random.Random(7)
        n = 8
        dense = [[0.0] * n for _ in range(n)]
        for i in range(n):
            for j in range(i + 1, n):
                dense[i][j] = dense[j][i] = rng.uniform(-1.0, 1.0)
        for i in range(n):
            dense[i][i] = n + sum(abs(dense[i][j]) for j in range(n) if j != i)
        rows, cols, vals = [], [], []
        for i in range(n):
            for j in range(n):
                if dense[i][j] != 0.0:
                    rows.append(i); cols.append(j); vals.append(dense[i][j])
        A = SparseMatrix(n, n, rows, cols, vals)
        b = [rng.uniform(-1.0, 1.0) for _ in range(n)]
        x_ref = solve_dense(dense, b)
        result = CGSolver(A, rtol=1e-12, atol=1e-14).solve(b, max_iter=200)
        self.assertEqual(result.status, STATUS_CONVERGED)
        err = max(abs(a - c) for a, c in zip(result.x, x_ref))
        self.assertLess(err, 1e-8)


class TestNonSPDInputs(unittest.TestCase):
    def test_asymmetric_input_returns_numeric_evidence(self):
        A = SparseMatrix(2, 2, [0, 0, 1, 1], [0, 1, 0, 1],
                         [2.0, -1.0, -0.5, 2.0])
        solver = CGSolver(A, check_symmetry=True, symmetry_tol=1e-12)
        result = solver.solve([1.0, 1.0])
        self.assertEqual(result.status, STATUS_ASYMMETRIC)
        self.assertFalse(result.converged)
        ev = result.evidence
        self.assertAlmostEqual(ev["max_abs_asymmetry"], 0.5)
        self.assertEqual((ev["row"], ev["col"]), (0, 1))
        self.assertAlmostEqual(ev["value_ij"], -1.0)
        self.assertAlmostEqual(ev["value_ji"], -0.5)

    def test_indefinite_matrix_returns_numeric_evidence(self):
        A = diagonal_matrix([1.0, -2.0])
        result = CGSolver(A).solve([1.0, 1.0])
        self.assertEqual(result.status, STATUS_INDEFINITE)
        self.assertFalse(result.converged)
        self.assertLessEqual(result.evidence["pAp"], 0.0)
        self.assertAlmostEqual(result.evidence["pAp"], -1.0)
        self.assertEqual(result.evidence["iteration"], 1)


class TestMaxIterIsNotNoSolution(unittest.TestCase):
    def test_max_iter_returns_best_iterate_not_a_failure(self):
        n = 50
        A = poisson_1d(n)
        rng = random.Random(9)
        b = [rng.uniform(-1.0, 1.0) for _ in range(n)]
        result = CGSolver(A, rtol=1e-14, atol=0.0).solve(b, max_iter=3)
        self.assertEqual(result.status, STATUS_MAX_ITER)
        self.assertFalse(result.converged)
        self.assertIsNotNone(result.x)
        self.assertEqual(len(result.x), n)
        self.assertGreater(result.true_residual_norm, result.tolerance)
        # The residual still decreased: this is "not converged yet", not
        # "no solution exists".
        self.assertLess(result.true_residual_norm, result.initial_residual_norm)


if __name__ == "__main__":
    unittest.main()
