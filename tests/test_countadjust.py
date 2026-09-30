"""unittest suite for countadjust (exact rational WLS on count DAGs)."""

import random
import unittest
from fractions import Fraction

from countadjust import (
    CountDAG, Status, WLSSolver, enumerate_active_sets,
)
from countadjust.rational_linalg import dot


def two_leaf_tree(w_root=1, w_a=1, w_b=1, y=(10, 3, 5)):
    m = CountDAG()
    m.add_node("root", observed=y[0], weight=w_root)
    m.add_node("a", observed=y[1], weight=w_a)
    m.add_node("b", observed=y[2], weight=w_b)
    m.add_parent_equals_children("root", ["a", "b"])
    return m


class TreeAdjustmentTest(unittest.TestCase):
    def test_two_leaves_sum_equals_root(self):
        r = WLSSolver(two_leaf_tree()).solve()
        self.assertEqual(r.status, Status.OPTIMAL)
        x = dict(zip(["root", "a", "b"], r.x))
        self.assertEqual(x["a"] + x["b"], x["root"])
        self.assertEqual(x["root"], Fraction(28, 3))
        self.assertEqual(x["a"], Fraction(11, 3))
        self.assertEqual(x["b"], Fraction(17, 3))
        self.assertEqual(r.objective, Fraction(2, 3))

    def test_kkt_certificate_exact(self):
        r = WLSSolver(two_leaf_tree()).solve()
        cert = r.certificate
        self.assertTrue(cert.is_valid())
        self.assertTrue(all(v == 0 for v in cert.stationarity_residual))
        self.assertTrue(all(v == 0 for v in cert.primal_residual))

    def test_weight_zero_means_free_variable(self):
        # w_root = 0: the root observation is ignored entirely; the root
        # is determined by the constraint alone, objective reaches 0.
        r = WLSSolver(two_leaf_tree(w_root=0, y=(100, 3, 5))).solve()
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.x, [Fraction(8), Fraction(3), Fraction(5)])
        self.assertEqual(r.objective, Fraction(0))

    def test_zero_weight_singularity_detected(self):
        # No constraints and w_b = 0: x_b is undetermined.
        m = CountDAG()
        m.add_node("a", observed=1, weight=1)
        m.add_node("b", observed=2, weight=0)
        r = WLSSolver(m).solve()
        self.assertEqual(r.status, Status.SINGULAR_ZERO_WEIGHT)
        self.assertFalse(r.is_optimal)
        d = r.singularity_direction
        self.assertEqual(d, [Fraction(0), Fraction(1)])
        self.assertEqual(r.undetermined_variables, ("b",))

    def test_negative_weight_rejected(self):
        m = CountDAG()
        with self.assertRaises(ValueError):
            m.add_node("a", observed=1, weight=-1)


class SharedLeafDAGTest(unittest.TestCase):
    def redundant_dag(self):
        # root = p1 + p2; p1 = a + b; p2 = c + d; root = a + b + c + d
        # The 4th constraint is redundant (rank 3).
        m = CountDAG()
        for name, y in [("root", 20), ("p1", 9), ("p2", 12),
                        ("a", 4), ("b", 6), ("c", 5), ("d", 8)]:
            m.add_node(name, observed=y, weight=1)
        m.add_parent_equals_children("root", ["p1", "p2"])
        m.add_parent_equals_children("p1", ["a", "b"])
        m.add_parent_equals_children("p2", ["c", "d"])
        m.add_parent_equals_children("root", ["a", "b", "c", "d"])
        return m

    def test_redundant_constraints_handled(self):
        r = WLSSolver(self.redundant_dag()).solve()
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertTrue(r.certificate.is_valid())
        x = dict(zip(["root", "p1", "p2", "a", "b", "c", "d"], r.x))
        self.assertEqual(x["p1"] + x["p2"], x["root"])
        self.assertEqual(x["a"] + x["b"], x["p1"])
        self.assertEqual(x["c"] + x["d"], x["p2"])

    def test_shared_leaf_dag(self):
        # Two parents share one leaf: p1 = a + s, p2 = s + b.
        m = CountDAG()
        for name, y in [("p1", 10), ("p2", 10), ("a", 3), ("s", 4), ("b", 3)]:
            m.add_node(name, observed=y, weight=1)
        m.add_parent_equals_children("p1", ["a", "s"])
        m.add_parent_equals_children("p2", ["s", "b"])
        r = WLSSolver(m).solve()
        self.assertEqual(r.status, Status.OPTIMAL)
        x = dict(zip(["p1", "p2", "a", "s", "b"], r.x))
        self.assertEqual(x["a"] + x["s"], x["p1"])
        self.assertEqual(x["s"] + x["b"], x["p2"])
        # Cross-check against brute-force enumeration.
        enum = enumerate_active_sets(m, nonneg=False)
        self.assertEqual(enum[0], r.objective)


class FixedCountTest(unittest.TestCase):
    def test_feasible_fixed_counts(self):
        m = two_leaf_tree()
        m.fix("b", 2)
        r = WLSSolver(m).solve()
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.x, [Fraction(15, 2), Fraction(11, 2), Fraction(2)])
        self.assertTrue(r.certificate.is_valid())

    def test_infeasible_fixed_counts_with_certificate(self):
        m = two_leaf_tree()
        m.fix("root", 10)
        m.fix("a", 3)
        m.fix("b", 4)  # 3 + 4 != 10
        r = WLSSolver(m).solve()
        self.assertEqual(r.status, Status.INFEASIBLE)
        self.assertFalse(r.is_optimal)
        mu = r.infeasibility_certificate
        A, b = m.build_system()
        # Verify the Farkas certificate exactly: mu^T A = 0, mu^T b != 0.
        for col in range(m.nvars):
            self.assertEqual(
                sum(mu[k] * A[k][col] for k in range(len(A))), 0)
        self.assertNotEqual(dot(mu, b), 0)

    def test_negative_fixed_count_infeasible_under_nonneg(self):
        m = two_leaf_tree()
        m.fix("a", -1)
        r = WLSSolver(m, nonneg=True).solve()
        self.assertEqual(r.status, Status.INFEASIBLE)


class NonNegativityTest(unittest.TestCase):
    def test_active_set_hits_bound(self):
        m = two_leaf_tree(y=(1, 5, -3))
        r = WLSSolver(m, nonneg=True).solve()
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.x, [Fraction(3), Fraction(3), Fraction(0)])
        self.assertEqual(r.objective, Fraction(17, 2))
        self.assertEqual(r.active_set, frozenset({2}))
        cert = r.certificate
        self.assertTrue(cert.is_valid())
        self.assertGreaterEqual(cert.bound_multipliers[2], 0)

    def test_unconstrained_solution_already_nonneg(self):
        r = WLSSolver(two_leaf_tree(), nonneg=True).solve()
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.active_set, frozenset())
        self.assertEqual(r.x[0], Fraction(28, 3))

    def test_max_iter_is_not_optimal(self):
        m = two_leaf_tree(y=(1, 5, -3))
        r = WLSSolver(m, nonneg=True, max_iter=0).solve()
        self.assertEqual(r.status, Status.MAX_ITER)
        self.assertFalse(r.is_optimal)
        self.assertIsNone(r.certificate)


class EnumerationCrossCheckTest(unittest.TestCase):
    def test_random_dags_match_enumeration(self):
        rng = random.Random(20261001)
        for trial in range(30):
            n_leaves = rng.randint(2, 4)
            m = CountDAG()
            m.add_node("root", observed=rng.randint(-5, 15), weight=1)
            leaves = []
            for i in range(n_leaves):
                name = f"L{i}"
                leaves.append(name)
                w = rng.choice([1, 1, 1, 2, 3])
                m.add_node(name, observed=rng.randint(-6, 10), weight=w)
            m.add_parent_equals_children("root", leaves)
            if rng.random() < 0.5:
                # redundant row: root again equals a re-grouped sum
                m.add_parent_equals_children("root", leaves)
            nonneg = rng.random() < 0.7
            r = WLSSolver(m, nonneg=nonneg).solve()
            self.assertEqual(r.status, Status.OPTIMAL, msg=f"trial {trial}")
            self.assertTrue(r.certificate.is_valid())
            enum = enumerate_active_sets(m, nonneg=nonneg)
            self.assertEqual(enum[0], r.objective, msg=f"trial {trial}")
            self.assertIn(r.x, enum[1], msg=f"trial {trial}")


class WarmStartTest(unittest.TestCase):
    def test_local_revision_warm_start_reverified(self):
        m = two_leaf_tree(y=(1, 5, -3))
        solver = WLSSolver(m, nonneg=True)
        cold = solver.solve()
        self.assertEqual(cold.status, Status.OPTIMAL)
        warm = solver.revise(observed_updates={"b": 7})
        self.assertTrue(warm.warm_started)
        self.assertEqual(warm.status, Status.OPTIMAL)
        self.assertTrue(warm.certificate.is_valid())
        # Cold re-solve of the revised problem must agree exactly.
        m2 = two_leaf_tree(y=(1, 5, 7))
        cold2 = WLSSolver(m2, nonneg=True).solve()
        self.assertEqual(warm.x, cold2.x)
        self.assertEqual(warm.objective, cold2.objective)

    def test_warm_start_with_wrong_active_set_still_correct(self):
        m = two_leaf_tree(y=(1, 5, -3))
        solver = WLSSolver(m, nonneg=True)
        solver.solve()
        # Revision flips which bound is active; warm start must recover.
        r = solver.revise(observed_updates={"a": -4, "b": 6})
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertTrue(r.certificate.is_valid())
        enum = enumerate_active_sets(
            two_leaf_tree(y=(1, -4, 6)), nonneg=True)
        self.assertEqual(enum[0], r.objective)


if __name__ == "__main__":
    unittest.main()
