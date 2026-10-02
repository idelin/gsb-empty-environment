import unittest
from fractions import Fraction

from dagadjust.certificates import build_certificates
from dagadjust.enumeration import enumerate_active_sets
from dagadjust.linalg import independent_rows
from dagadjust.problem import AdjustmentProblem, CountDAG
from dagadjust.solver import (
    STATUS_INFEASIBLE,
    STATUS_NOT_CONVERGED,
    STATUS_OPTIMAL,
    STATUS_SINGULAR,
    solve,
)

F = Fraction


def tree3():
    return CountDAG.from_children(3, {0: (1, 2)})


class TestEqualityWLS(unittest.TestCase):
    def test_two_leaves_sum_equals_root(self):
        # Closed form: min (r-10)^2+(a-3)^2+(b-5)^2 s.t. r = a+b
        # gives a = 11/3, b = 17/3, r = 28/3.
        p = AdjustmentProblem.create(tree3(), [10, 3, 5], [1, 1, 1])
        r = solve(p)
        self.assertEqual(r.status, STATUS_OPTIMAL)
        self.assertTrue(r.is_optimal)
        self.assertEqual(r.x, [F(28, 3), F(11, 3), F(17, 3)])
        self.assertEqual(r.x[0], r.x[1] + r.x[2])
        self.assertEqual(r.residuals, [F(-2, 3), F(2, 3), F(2, 3)])
        self.assertEqual(r.objective, F(2, 3))  # 1/2 * sum of squared residuals

    def test_weight_zero_means_observation_ignored(self):
        # w_2 = 0: x_2 is free and fixed only by the balance constraint.
        p = AdjustmentProblem.create(tree3(), [10, 3, 999], [1, 1, 0])
        r = solve(p)
        self.assertTrue(r.is_optimal)
        self.assertEqual(r.x, [F(10), F(3), F(7)])
        self.assertEqual(r.zero_weight_variables, [2])

    def test_fixed_counts_respected(self):
        # x1 fixed to 4: min (r-10)^2 + b^2 with r = 4 + b -> b = 3, r = 7.
        p = AdjustmentProblem.create(tree3(), [10, 0, 0], [1, 1, 1], fixed={1: 4})
        r = solve(p)
        self.assertTrue(r.is_optimal)
        self.assertEqual(r.x, [F(7), F(4), F(3)])
        self.assertEqual(r.objective, F(17))

    def test_redundant_constraints_detected(self):
        # Fixing every count makes the balance row x0 = x1 + x2 redundant.
        p = AdjustmentProblem.create(
            tree3(), [0, 0, 0], [1, 1, 1], fixed={0: 10, 1: 4, 2: 6}
        )
        r = solve(p)
        self.assertTrue(r.is_optimal)
        # Greedy row reduction keeps the balance row and drops the last
        # fixed row: e2 = e0 - e1 - (balance row).
        self.assertEqual(r.redundant_constraints, [3])
        self.assertEqual(r.x, [F(10), F(4), F(6)])
        self.assertEqual(r.certificates.max_eq_residual, 0)


class TestNonnegActiveSet(unittest.TestCase):
    def test_negative_unconstrained_solution_clamped(self):
        # Unconstrained optimum has b = -7/3; nonneg optimum is [3, 3, 0].
        p = AdjustmentProblem.create(tree3(), [1, 5, -2], [1, 1, 1], nonneg=True)
        r = solve(p)
        self.assertTrue(r.is_optimal)
        self.assertEqual(r.x, [F(3), F(3), F(0)])
        self.assertEqual(r.active_set, [2])
        # min_dual ranges over all mu_i; inactive bounds have mu_i = 0.
        self.assertEqual(r.certificates.min_dual, F(0))
        self.assertTrue(r.certificates.verified)
        self.assertEqual(r.iterations, 2)

    def test_shared_leaf_dag_nonneg(self):
        dag = CountDAG.from_children(6, {0: (1, 2), 1: (3, 4), 2: (4, 5)})
        p = AdjustmentProblem.create(
            dag, [4, 9, -3, 5, 6, -2], [1, 1, 1, 1, 1, 1], nonneg=True
        )
        r = solve(p)
        self.assertTrue(r.is_optimal)
        best = enumerate_active_sets(p)
        self.assertIsNotNone(best)
        self.assertEqual(r.objective, best[0])
        self.assertEqual(r.x, best[1])


class TestInfeasibilityAndSingularity(unittest.TestCase):
    def test_inconsistent_fixed_counts(self):
        p = AdjustmentProblem.create(
            tree3(), [0, 0, 0], [1, 1, 1], fixed={0: 10, 1: 7, 2: 8}
        )
        r = solve(p)
        self.assertEqual(r.status, STATUS_INFEASIBLE)
        self.assertFalse(r.is_optimal)
        self.assertIsNone(r.x)

    def test_fixed_counts_infeasible_under_nonneg(self):
        # x0 = 1, x1 = 5 forces x2 = -4 < 0.
        p = AdjustmentProblem.create(
            tree3(), [0, 0, 0], [1, 1, 1], fixed={0: 1, 1: 5}, nonneg=True
        )
        r = solve(p)
        self.assertEqual(r.status, STATUS_INFEASIBLE)
        self.assertIsNone(enumerate_active_sets(p))

    def test_negative_fixed_value_under_nonneg(self):
        p = AdjustmentProblem.create(
            tree3(), [0, 0, 0], [1, 1, 1], fixed={2: -1}, nonneg=True
        )
        r = solve(p)
        self.assertEqual(r.status, STATUS_INFEASIBLE)

    def test_zero_weight_singularity_detected(self):
        # Node 3 is isolated and has weight 0: nothing determines x3.
        dag = CountDAG.from_children(4, {0: (1, 2)})
        p = AdjustmentProblem.create(dag, [10, 4, 5, 0], [1, 1, 1, 0])
        r = solve(p)
        self.assertEqual(r.status, STATUS_SINGULAR)
        self.assertIn("x3", r.message)
        self.assertFalse(r.is_optimal)


class TestConvergenceSemantics(unittest.TestCase):
    def test_iteration_cap_means_not_optimal(self):
        p = AdjustmentProblem.create(tree3(), [1, 5, -2], [1, 1, 1], nonneg=True)
        r = solve(p, max_iterations=1)  # needs 2 EQP solves
        self.assertEqual(r.status, STATUS_NOT_CONVERGED)
        self.assertFalse(r.is_optimal)
        self.assertIsNone(r.x)
        self.assertIn("NOT certified optimal", r.message)

    def test_zero_iteration_budget(self):
        p = AdjustmentProblem.create(tree3(), [10, 3, 5], [1, 1, 1])
        r = solve(p, max_iterations=0)
        self.assertEqual(r.status, STATUS_NOT_CONVERGED)
        self.assertFalse(r.is_optimal)


class TestWarmStart(unittest.TestCase):
    def test_local_revision_warm_start_reverified(self):
        p = AdjustmentProblem.create(tree3(), [1, 5, -2], [1, 1, 1], nonneg=True)
        first = solve(p)
        self.assertEqual(first.active_set, [2])
        revised = p.with_observation(1, 6)
        cold = solve(revised)
        warm = solve(revised, warm_start=first)
        self.assertTrue(cold.is_optimal)
        self.assertTrue(warm.is_optimal)
        self.assertEqual(warm.x, cold.x)
        self.assertEqual(warm.x, [F(7, 2), F(7, 2), F(0)])
        self.assertEqual(warm.iterations, 1)  # warm start saved one EQP
        self.assertEqual(cold.iterations, 2)
        # Certificates are recomputed, not inherited.
        self.assertIsNot(warm.certificates, first.certificates)
        self.assertTrue(warm.certificates.verified)


class TestCertificates(unittest.TestCase):
    def test_tampered_point_fails_verification(self):
        p = AdjustmentProblem.create(tree3(), [10, 3, 5], [1, 1, 1])
        r = solve(p)
        self.assertTrue(r.certificates.verified)
        rows, rhs, _ = p.constraints()
        reduced, kept, _ = independent_rows(rows)
        bad_x = list(r.x)
        bad_x[0] += 1  # breaks x0 = x1 + x2
        certs = build_certificates(p, rows, rhs, reduced, bad_x,
                                   [F(0)] * len(reduced), [F(0)] * 3)
        self.assertFalse(certs.verified)
        self.assertGreater(certs.max_eq_residual, 0)


class TestEnumerationCrossCheck(unittest.TestCase):
    CASES = [
        # (children, y, w, nonneg)
        ({0: (1, 2)}, [10, 3, 5], [1, 1, 1], False),
        ({0: (1, 2)}, [1, 5, -2], [1, 1, 1], True),
        ({0: (1, 2)}, [-4, -1, -2], [2, 1, 3], True),
        ({0: (1, 2), 1: (3, 4), 2: (5, 6)}, [105, 52, 55, 25, 28, 27, 26],
         [1, 1, 1, 2, 2, 2, 2], False),
        ({0: (1, 2), 1: (3, 4), 2: (5, 6)}, [10, 3, 9, -1, 4, 8, -3],
         [1, 2, 1, 1, 1, 2, 1], True),
        ({0: (1, 2), 1: (3, 4), 2: (4, 5)}, [20, 11, 9, 5, 6, 3],
         [1, 2, 1, 1, 3, 1], False),
        ({0: (1, 2), 1: (3, 4), 2: (4, 5)}, [4, 9, -3, 5, 6, -2],
         [1, 1, 1, 1, 1, 1], True),
        ({0: (1, 2), 1: (2, 3)}, [7, 2, 1, 4], [3, 1, 2, 1], True),
    ]

    def test_solver_matches_brute_force_enumeration(self):
        for children, y, w, nonneg in self.CASES:
            with self.subTest(children=children, nonneg=nonneg):
                dag = CountDAG.from_children(len(y), children)
                p = AdjustmentProblem.create(dag, y, w, nonneg=nonneg)
                r = solve(p)
                best = enumerate_active_sets(p)
                self.assertIsNotNone(best)
                self.assertTrue(r.is_optimal)
                self.assertEqual(r.objective, best[0])
                self.assertEqual(r.x, best[1])
                self.assertEqual(r.active_set, best[2])


if __name__ == "__main__":
    unittest.main()
