"""unittest suite for the incremental weighted least-squares solver.

Run with:  python3.11 -m unittest -v
"""

import random
import unittest

from ils_linalg import mat_vec
from ils_reference import (
    exact_normal_equations,
    exact_solve,
    max_relative_error,
)
from ils_solver import (
    DimensionError,
    DuplicateSampleError,
    IncrementalLeastSquares,
    InvalidWeightError,
    NonFiniteError,
    UnknownSampleError,
    UpdateError,
)


def exact_solution(observations, n, lam):
    M, z = exact_normal_equations(list(observations), n, lam)
    return exact_solve(M, z)


class SolverTestCase(unittest.TestCase):
    def assertSolutionMatchesExact(self, solver, tol):
        sol = solver.solve()
        self.assertEqual(sol.status, "unique")
        exact = exact_solution(solver.observations.values(),
                               solver.n, solver.regularization)
        err = max_relative_error(sol.x, exact)
        self.assertLess(err, tol,
                        "relative error %.3e exceeds %.3e" % (err, tol))
        return err

    @staticmethod
    def max_matrix_diff(A, B):
        return max(abs(a - b) for ra, rb in zip(A, B)
                   for a, b in zip(ra, rb))


class TestBasicSolve(SolverTestCase):
    def test_add_solve_matches_exact_reference(self):
        rng = random.Random(42)
        solver = IncrementalLeastSquares(4)
        for i in range(10):
            row = [rng.uniform(-1, 1) for _ in range(4)]
            solver.add("s%d" % i, row, rng.uniform(-5, 5))
        self.assertSolutionMatchesExact(solver, 1e-9)

    def test_add_then_retract_restores_matrix(self):
        rng = random.Random(7)
        solver = IncrementalLeastSquares(3, regularization=0.25)
        for i in range(4):
            solver.add(i, [rng.uniform(-1, 1) for _ in range(3)],
                       rng.uniform(-1, 1))
        before = solver.normal_matrix()
        z_before = solver.rhs()
        solver.add("extra", [0.3, -0.8, 1.2], 2.0, weight=1.7)
        solver.retract("extra")
        self.assertLess(self.max_matrix_diff(before,
                                             solver.normal_matrix()), 1e-10)
        z_diff = max(abs(a - b) for a, b in zip(z_before, solver.rhs()))
        self.assertLess(z_diff, 1e-10)

    def test_retract_in_reverse_order_matches_reference(self):
        rng = random.Random(11)
        solver = IncrementalLeastSquares(3, regularization=1e-6)
        ids = []
        for i in range(8):
            sid = "r%d" % i
            solver.add(sid, [rng.uniform(-1, 1) for _ in range(3)],
                       rng.uniform(-2, 2), weight=rng.uniform(0.5, 2.0))
            ids.append(sid)
        for sid in reversed(ids[:5]):
            solver.retract(sid)
            self.assertSolutionMatchesExact(solver, 1e-8)


class TestValidationAndRollback(SolverTestCase):
    def test_retract_unknown_sample_raises_and_preserves_state(self):
        solver = IncrementalLeastSquares(2, regularization=1.0)
        solver.add("a", [1.0, 0.0], 1.0)
        before = solver.normal_matrix()
        with self.assertRaises(UnknownSampleError):
            solver.retract("missing")
        self.assertEqual(before, solver.normal_matrix())
        self.assertEqual(len(solver), 1)

    def test_duplicate_id_rejected(self):
        solver = IncrementalLeastSquares(2, regularization=1.0)
        solver.add("a", [1.0, 0.0], 1.0)
        with self.assertRaises(DuplicateSampleError):
            solver.add("a", [0.0, 1.0], 2.0)
        self.assertEqual(len(solver), 1)

    def test_zero_weight_registers_id_without_contribution(self):
        solver = IncrementalLeastSquares(2, regularization=0.5)
        solver.add("a", [1.0, 0.0], 1.0)
        before = solver.normal_matrix()
        solver.add("z", [5.0, 5.0], 9.0, weight=0.0)
        self.assertIn("z", solver)
        self.assertEqual(before, solver.normal_matrix())
        solver.retract("z")
        self.assertNotIn("z", solver)
        self.assertEqual(before, solver.normal_matrix())

    def test_negative_weight_rejected(self):
        solver = IncrementalLeastSquares(2, regularization=1.0)
        solver.add("a", [1.0, 2.0], 1.0)
        before = solver.normal_matrix()
        with self.assertRaises(InvalidWeightError):
            solver.add("bad", [1.0, 1.0], 1.0, weight=-0.5)
        self.assertNotIn("bad", solver)
        self.assertEqual(before, solver.normal_matrix())
        self.assertTrue(solver.is_positive_definite)
        self.assertSolutionMatchesExact(solver, 1e-12)

    def test_dimension_mismatch_rejected(self):
        solver = IncrementalLeastSquares(3)
        with self.assertRaises(DimensionError):
            solver.add("x", [1.0, 2.0], 1.0)

    def test_nonfinite_rejected(self):
        solver = IncrementalLeastSquares(2)
        with self.assertRaises(NonFiniteError):
            solver.add("x", [1.0, float("nan")], 1.0)
        with self.assertRaises(NonFiniteError):
            solver.add("y", [1.0, 1.0], float("inf"))
        self.assertEqual(len(solver), 0)

    def test_atomic_rollback_on_failed_downdate(self):
        # An absurdly large rank tolerance forces the downdate to fail
        # even though the remaining system keeps full rank; the update
        # must roll back atomically and keep the old factor.
        solver = IncrementalLeastSquares(2, rank_tol=0.9)
        solver.add("a", [1.0, 0.0], 1.0)
        solver.add("b", [0.0, 1.0], 2.0)
        solver.add("c", [1.0, 1.0], 3.0)
        before = solver.normal_matrix()
        with self.assertRaises(UpdateError):
            solver.retract("c")
        self.assertIn("c", solver)
        self.assertEqual(solver.stats["rollbacks"], 1)
        self.assertEqual(self.max_matrix_diff(before,
                                              solver.normal_matrix()), 0.0)
        self.assertSolutionMatchesExact(solver, 1e-9)


class TestRankDeficiency(SolverTestCase):
    def test_rank_deficient_returns_solution_space(self):
        solver = IncrementalLeastSquares(2)
        solver.add("a", [1.0, 0.0], 3.0)
        solver.add("b", [0.0, 1.0], 4.0)
        self.assertEqual(solver.solve().status, "unique")
        self.assertTrue(solver.is_positive_definite)

        solver.retract("b")  # rank drops 2 -> 1
        self.assertFalse(solver.is_positive_definite)
        sol = solver.solve()
        self.assertEqual(sol.status, "deficient")
        self.assertEqual(sol.rank, 1)
        self.assertAlmostEqual(sol.x[0], 3.0)
        self.assertAlmostEqual(sol.x[1], 0.0)
        self.assertEqual(len(sol.null_space), 1)

        # null-space basis vector really lies in ker(M)
        M, _ = exact_normal_equations(list(solver.observations.values()),
                                      2, 0.0)
        Mf = [[float(c) for c in r] for r in M]
        Mv = mat_vec(Mf, sol.null_space[0])
        self.assertLess(max(abs(c) for c in Mv), 1e-9)
        # particular solution satisfies the normal equations
        self.assertLess(solver.residual(sol.x), 1e-9)

        # adding a row restores full rank and the factor path
        solver.add("b2", [0.0, 1.0], 5.0)
        self.assertTrue(solver.is_positive_definite)
        sol = solver.solve()
        self.assertEqual(sol.status, "unique")
        self.assertAlmostEqual(sol.x[0], 3.0)
        self.assertAlmostEqual(sol.x[1], 5.0)

    def test_regularization_keeps_solution_unique(self):
        solver = IncrementalLeastSquares(2, regularization=1e-6)
        solver.add("a", [1.0, 0.0], 3.0)
        solver.add("b", [0.0, 1.0], 4.0)
        solver.retract("b")  # would drop rank, but lambda I keeps M PD
        self.assertTrue(solver.is_positive_definite)
        self.assertSolutionMatchesExact(solver, 1e-9)


class TestSlidingWindow(SolverTestCase):
    def test_sliding_window_matches_reference(self):
        rng = random.Random(123)
        solver = IncrementalLeastSquares(3, regularization=1e-6)
        window = []
        worst = 0.0
        for step in range(14):
            sid = "w%d" % step
            row = [rng.uniform(-1, 1) for _ in range(3)]
            solver.add(sid, row, rng.uniform(-2, 2),
                       weight=rng.uniform(0.5, 2.0))
            window.append(sid)
            if len(window) > 5:
                solver.retract(window.pop(0))
            worst = max(worst, self.assertSolutionMatchesExact(solver, 1e-6))
        print("\n[report] sliding-window max relative error: %.3e" % worst)


class TestNumericalStress(SolverTestCase):
    def test_large_offset_targets(self):
        rng = random.Random(9)
        solver = IncrementalLeastSquares(3)
        offset = 1.0e6
        for i in range(8):
            row = [rng.uniform(0.5, 1.5) for _ in range(3)]
            solver.add(i, row, offset + rng.uniform(-1, 1))
        err = self.assertSolutionMatchesExact(solver, 1e-7)
        print("\n[report] large-offset targets relative error: %.3e" % err)

    def test_near_correlated_columns(self):
        eps = 1e-6
        solver = IncrementalLeastSquares(2, regularization=1e-6)
        # unregularized exact solution is x = (1, 2)
        solver.add("r1", [1.0, 1.0], 3.0)
        solver.add("r2", [1.0, 1.0 + eps], 3.0 + 2 * eps)
        solver.add("r3", [1.0, 1.0 - eps], 3.0 - 2 * eps)
        err = self.assertSolutionMatchesExact(solver, 5e-4)
        print("\n[report] near-correlated columns relative error: %.3e" % err)

    def test_tiny_weights(self):
        solver = IncrementalLeastSquares(2)
        solver.add("a", [1.0, 0.0], 1.0)
        solver.add("b", [0.0, 1.0], 2.0, weight=1e-12)
        solver.add("c", [1.0, 1.0], 3.0)
        err1 = self.assertSolutionMatchesExact(solver, 1e-9)
        # tiny-weight downdate must not trigger a false rank drop
        solver.retract("b")
        self.assertTrue(solver.is_positive_definite)
        err2 = self.assertSolutionMatchesExact(solver, 1e-9)
        print("\n[report] tiny-weight relative error: %.3e"
              % max(err1, err2))


class TestVerification(SolverTestCase):
    def test_periodic_verification_runs_and_passes(self):
        rng = random.Random(5)
        solver = IncrementalLeastSquares(3, regularization=0.1,
                                       verify_interval=4)
        for i in range(11):
            solver.add(i, [rng.uniform(-1, 1) for _ in range(3)],
                       rng.uniform(-1, 1))
        self.assertGreaterEqual(solver.stats["verifications"], 2)
        report = solver.verify()
        self.assertTrue(report.ok)
        self.assertLess(report.factor_deviation, 1e-8)
        self.assertLess(report.rhs_deviation, 1e-8)
        self.assertFalse(report.refactorized)

    def test_residual_against_raw_observations(self):
        rng = random.Random(21)
        solver = IncrementalLeastSquares(3, regularization=1e-4)
        for i in range(6):
            solver.add(i, [rng.uniform(-1, 1) for _ in range(3)],
                       rng.uniform(-1, 1))
        self.assertLess(solver.residual(), 1e-8)


if __name__ == "__main__":
    unittest.main()
