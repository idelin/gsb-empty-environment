"""Runnable unittest suite for the sparse PCG solver.

Run with:  python3.11 -m unittest discover -s tests -v
"""

import math
import random
import unittest

from sparse_cg import (
    CGSolver,
    CGStatus,
    DiagonalPreconditioner,
    SparseMatrix,
    VersionMismatchError,
    dense_solve,
)


# ------------------------------------------------------------------ helpers

def dense_to_sparse(a):
    n = len(a)
    indptr, indices, values = [0], [], []
    for row in a:
        for j, v in enumerate(row):
            if v != 0.0:
                indices.append(j)
                values.append(v)
        indptr.append(len(indices))
    return SparseMatrix(n, n, indptr, indices, values)


def random_spd_dense(n, seed):
    """A = M'M + n*I, guaranteed SPD."""
    rng = random.Random(seed)
    m = [[rng.uniform(-1.0, 1.0) for _ in range(n)] for _ in range(n)]
    return [[sum(m[k][i] * m[k][j] for k in range(n)) + (n if i == j else 0.0)
             for j in range(n)] for i in range(n)]


def ill_conditioned_spd(seed=11, n=64):
    """Diagonal SPD: 60 eigenvalues in [1,100] plus 4 outliers up to 1e11.

    In finite precision the recursively updated CG residual plunges far
    below the explicitly computed (true) residual, which stalls near
    6e-5 -- exactly the situation where trusting the recurrence residual
    would declare a false convergence.
    """
    rng = random.Random(seed)
    diag = [rng.uniform(1.0, 100.0) for _ in range(n - 4)] + [1e10, 2e10, 5e10, 1e11]
    A = SparseMatrix(n, n, list(range(n + 1)), list(range(n)), diag)
    x_exact = [rng.uniform(0.5, 1.5) for _ in range(n)]
    b = A.matvec(x_exact)
    return A, b, x_exact


def logspread_spd(seed=7, n=64):
    """Diagonal SPD, eigenvalues log-spaced from 1 to 1e12: CG (with an
    identity preconditioner) cannot make real progress -- a genuine
    stagnation case."""
    rng = random.Random(seed)
    diag = [10.0 ** (12.0 * i / (n - 1)) for i in range(n)]
    A = SparseMatrix(n, n, list(range(n + 1)), list(range(n)), diag)
    x_exact = [rng.uniform(0.5, 1.5) for _ in range(n)]
    b = A.matvec(x_exact)
    return A, b, x_exact


def max_err(u, v):
    return max(abs(a - b) for a, b in zip(u, v))


# --------------------------------------------------------------------- tests

class TestExactInitialGuess(unittest.TestCase):
    def test_exact_x0_returns_in_zero_iterations(self):
        A = dense_to_sparse(random_spd_dense(10, seed=1))
        rng = random.Random(2)
        x_exact = [rng.uniform(-1.0, 1.0) for _ in range(10)]
        b = A.matvec(x_exact)
        result = CGSolver(rtol=1e-12, atol=1e-14).solve(A, b, x0=x_exact)
        self.assertIs(result.status, CGStatus.CONVERGED)
        self.assertEqual(result.iterations, 0)
        self.assertEqual(result.true_residual_norm, 0.0)
        self.assertEqual(result.x, x_exact)


class TestTrueResidualTermination(unittest.TestCase):
    def test_illconditioned_converges_only_on_true_residual(self):
        A, b, x_exact = ill_conditioned_spd()
        identity = DiagonalPreconditioner([1.0] * A.n_rows)
        solver = CGSolver(rtol=0.0, atol=1e-4, max_iter=5000,
                          recompute_interval=200)
        result = solver.solve(A, b, precond=identity)

        self.assertIs(result.status, CGStatus.CONVERGED)
        # Termination criterion is the true residual, by contract.
        self.assertLessEqual(result.true_residual_norm, result.tolerance)

        # The recurrence residual claimed convergence prematurely at least
        # once while the true residual was still above tolerance; a solver
        # trusting the recurrence would have stopped there, unconverged.
        events = result.premature_convergence_events
        self.assertGreaterEqual(len(events), 1)
        first = events[0]
        self.assertLessEqual(first["recurrence_residual_norm"],
                             first["tolerance"])
        self.assertGreater(first["true_residual_norm"], first["tolerance"])
        self.assertLess(first["iteration"], result.iterations)

        # And the answer is genuinely close to the exact solution.
        self.assertLess(max_err(result.x, x_exact), 1e-3)

    def test_stagnation_detected_on_true_residual_history(self):
        A, b, _ = logspread_spd()
        identity = DiagonalPreconditioner([1.0] * A.n_rows)
        solver = CGSolver(rtol=1e-10, atol=0.0, max_iter=100000,
                          recompute_interval=10, stagnation_window=4,
                          stagnation_min_reduction=0.5)
        result = solver.solve(A, b, precond=identity)
        self.assertIs(result.status, CGStatus.STAGNATED)
        self.assertGreater(result.true_residual_norm, result.tolerance)
        ev = result.evidence
        self.assertIn("norm_then", ev)
        self.assertIn("norm_now", ev)
        # concrete evidence: less than the required 2x reduction
        self.assertGreater(ev["norm_now"],
                           ev["norm_then"] * ev["min_reduction"])
        self.assertGreater(len(result.residual_history), ev["window"])


class TestCheckpointAndHotStart(unittest.TestCase):
    def setUp(self):
        self.n = 12
        self.A = dense_to_sparse(random_spd_dense(self.n, seed=3))
        rng = random.Random(4)
        self.b = [rng.uniform(-1.0, 1.0) for _ in range(self.n)]
        self.x_exact = dense_solve(self.A.to_dense(), self.b)

    def test_cancel_then_restart_from_checkpoint(self):
        solver = CGSolver(rtol=1e-10, atol=1e-12)
        cancelled = solver.solve(
            self.A, self.b,
            callback=lambda it, x, rnorm: it < 4)  # cancel at iteration 4
        self.assertIs(cancelled.status, CGStatus.CANCELLED)
        self.assertEqual(cancelled.iterations, 4)

        ckpt = solver.save_checkpoint()
        self.assertEqual(ckpt.matrix_version, self.A.version)
        self.assertEqual(ckpt.iteration, 4)

        x_warm = solver.restore_checkpoint(self.A, ckpt)  # same version: ok
        resumed = solver.solve(self.A, self.b, x0=x_warm)
        self.assertIs(resumed.status, CGStatus.CONVERGED)
        self.assertLess(max_err(resumed.x, self.x_exact), 1e-6)

    def test_matrix_change_after_save_rejects_stale_checkpoint(self):
        solver = CGSolver(rtol=1e-10, atol=1e-12)
        solver.solve(self.A, self.b,
                     callback=lambda it, x, rnorm: it < 3)
        ckpt = solver.save_checkpoint()

        old_version = self.A.version
        self.A.update_value(0, 0, self.A.diagonal()[0] * 1.5)  # bumps version
        self.assertNotEqual(self.A.version, old_version)

        with self.assertRaises(VersionMismatchError) as ctx:
            solver.restore_checkpoint(self.A, ckpt)
        ev = ctx.exception.evidence
        self.assertEqual(ev["checkpoint_matrix_version"], old_version)
        self.assertEqual(ev["current_matrix_version"], self.A.version)

        # Explicit hot start is allowed: x is reused, Krylov state is not.
        x_warm = solver.restore_checkpoint(self.A, ckpt,
                                           allow_matrix_change=True)
        precond = DiagonalPreconditioner.from_matrix(self.A)
        result = solver.solve(self.A, self.b, x0=x_warm, precond=precond)
        self.assertIs(result.status, CGStatus.CONVERGED)
        self.assertTrue(result.hot_start)
        x_new_exact = dense_solve(self.A.to_dense(), self.b)
        self.assertLess(max_err(result.x, x_new_exact), 1e-6)

    def test_reused_solution_reinitializes_krylov_state(self):
        solver = CGSolver(rtol=1e-10, atol=1e-12)
        first = solver.solve(self.A, self.b)
        self.assertIs(first.status, CGStatus.CONVERGED)

        # Perturb the rhs; reuse the old solution as x0.  The solver must
        # rebuild r/z/p from scratch instead of recycling old directions.
        b2 = list(self.b)
        b2[0] += 0.5
        second = solver.solve(self.A, b2, x0=first.x)

        expected_initial = math.sqrt(sum(
            (bi - ai) ** 2 for bi, ai in zip(b2, self.A.matvec(first.x))))
        self.assertAlmostEqual(second.initial_residual_norm,
                               expected_initial, places=12)
        self.assertGreater(second.initial_residual_norm, second.tolerance)
        self.assertIs(second.status, CGStatus.CONVERGED)
        x2_exact = dense_solve(self.A.to_dense(), b2)
        self.assertLess(max_err(second.x, x2_exact), 1e-6)

    def test_matrix_version_change_hot_start_with_precond_update(self):
        precond = DiagonalPreconditioner.from_matrix(self.A)
        solver = CGSolver(rtol=1e-10, atol=1e-12)
        first = solver.solve(self.A, self.b, precond=precond)
        self.assertIs(first.status, CGStatus.CONVERGED)
        self.assertFalse(first.hot_start)

        self.A.update_value(3, 3, self.A.diagonal()[3] * 1.25)
        precond.update_from_matrix(self.A)  # updatable diagonal preconditioner

        second = solver.solve(self.A, self.b, x0=first.x, precond=precond)
        self.assertTrue(second.hot_start)
        self.assertIs(second.status, CGStatus.CONVERGED)
        x_new_exact = dense_solve(self.A.to_dense(), self.b)
        self.assertLess(max_err(second.x, x_new_exact), 1e-6)


class TestNumericalEvidence(unittest.TestCase):
    def test_zero_preconditioner_entry_reported(self):
        # zero diagonal entry => Jacobi preconditioner singular
        A = SparseMatrix(3, 3, [0, 2, 4, 5], [0, 1, 0, 1, 2],
                         [2.0, 1.0, 1.0, 0.0, 3.0])
        result = CGSolver().solve(A, [1.0, 2.0, 3.0])
        self.assertIs(result.status, CGStatus.INVALID_PRECONDITIONER)
        self.assertEqual(result.evidence["zero_diagonal_index"], 1)

    def test_lost_positive_definiteness_reports_pAp(self):
        # symmetric but indefinite: p'Ap = -2 < 0 at the first iteration
        A = SparseMatrix(2, 2, [0, 1, 2], [0, 1], [2.0, -1.0])
        identity = DiagonalPreconditioner([1.0, 1.0])
        result = CGSolver().solve(A, [1.0, 2.0], precond=identity)
        self.assertIs(result.status, CGStatus.NOT_POSITIVE_DEFINITE)
        self.assertEqual(result.evidence["iteration"], 1)
        self.assertAlmostEqual(result.evidence["pAp"], -2.0)
        self.assertGreater(result.evidence["p_norm"], 0.0)

    def test_asymmetric_input_reports_defect(self):
        A = SparseMatrix(2, 2, [0, 2, 4], [0, 1, 0, 1], [2.0, 1.5, 0.5, 2.0])
        result = CGSolver(check_symmetry=True).solve(A, [1.0, 1.0])
        self.assertIs(result.status, CGStatus.ASYMMETRIC_INPUT)
        ev = result.evidence
        self.assertAlmostEqual(ev["max_defect"], 1.0)
        self.assertEqual({ev["row"], ev["col"]}, {0, 1})
        self.assertAlmostEqual(abs(ev["a_ij"] - ev["a_ji"]), 1.0)

    def test_failing_matvec_reported_with_iteration(self):
        A = dense_to_sparse(random_spd_dense(8, seed=5))
        b = [1.0] * 8
        calls = {"count": 0}

        def flaky_matvec(x):
            calls["count"] += 1
            if calls["count"] >= 3:
                raise RuntimeError("simulated matvec failure")
            return A.matvec(x)

        result = CGSolver(rtol=1e-12).solve(A, b, matvec=flaky_matvec)
        self.assertIs(result.status, CGStatus.MATVEC_FAILURE)
        self.assertEqual(result.evidence["iteration"], 2)
        self.assertIn("simulated matvec failure",
                      result.evidence["exception"])

    def test_max_iterations_is_not_no_solution(self):
        dense = random_spd_dense(10, seed=6)
        A = dense_to_sparse(dense)
        rng = random.Random(8)
        b = [rng.uniform(-1.0, 1.0) for _ in range(10)]
        result = CGSolver(rtol=1e-12, atol=1e-14,
                          max_iter=1).solve(A, b)
        # budget exhausted: a status of its own, NOT "no solution exists"
        self.assertIs(result.status, CGStatus.MAX_ITERATIONS)
        self.assertFalse(result.converged)
        self.assertIn("NOT", result.evidence["reason"])
        # the system is in fact uniquely solvable
        x_exact = dense_solve(dense, b)
        self.assertEqual(len(x_exact), 10)
        self.assertGreater(result.true_residual_norm, result.tolerance)


class TestDenseCrossCheck(unittest.TestCase):
    def test_matches_exact_elimination_on_small_spd(self):
        for seed in (10, 20, 30):
            n = 12
            dense = random_spd_dense(n, seed=seed)
            A = dense_to_sparse(dense)
            rng = random.Random(seed + 1)
            b = [rng.uniform(-5.0, 5.0) for _ in range(n)]
            result = CGSolver(rtol=1e-10, atol=1e-12).solve(A, b)
            self.assertIs(result.status, CGStatus.CONVERGED)
            x_exact = dense_solve(dense, b)
            self.assertLess(max_err(result.x, x_exact), 1e-6,
                            f"seed={seed}")
            self.assertLessEqual(result.true_residual_norm,
                                 result.tolerance)


class TestExplicitInputValidation(unittest.TestCase):
    def test_bad_indptr_rejected(self):
        with self.assertRaises(ValueError):
            SparseMatrix(3, 3, [0, 1, 2], [0, 1], [1.0, 2.0])

    def test_bad_index_range_rejected(self):
        with self.assertRaises(ValueError):
            SparseMatrix(2, 2, [0, 1, 2], [0, 5], [1.0, 2.0])

    def test_nonsquare_rejected_by_solver(self):
        A = SparseMatrix(2, 3, [0, 1, 2], [0, 1], [1.0, 2.0])
        with self.assertRaises(ValueError):
            CGSolver().solve(A, [1.0, 1.0])

    def test_preconditioner_update_size_mismatch(self):
        precond = DiagonalPreconditioner([1.0, 1.0])
        with self.assertRaises(ValueError):
            precond.update([1.0, 1.0, 1.0])


if __name__ == "__main__":
    unittest.main()
