"""Unit tests (stdlib unittest only): python3 -m unittest test_rr -v"""

import math
import random
import unittest

import exact
import linalg
import randomized_response as rr
from estimate import ChannelGroup, estimate_distribution, project_simplex


class TestParameterValidation(unittest.TestCase):
    def test_invalid_k(self):
        for bad in (0, -1, 1.5, "2", True, None):
            with self.assertRaises(ValueError, msg=f"k={bad!r}"):
                rr.kary_channel(bad, 1.0)

    def test_invalid_epsilon(self):
        for bad in (-1.0, -0.5, float("nan"), float("inf"), "x", True, None):
            with self.assertRaises(ValueError, msg=f"eps={bad!r}"):
                rr.kary_channel(2, bad)

    def test_invalid_binary_params(self):
        with self.assertRaises(ValueError):
            rr.binary_channel(p=1.5, q=0.5)
        with self.assertRaises(ValueError):
            rr.binary_channel(p=-0.1, q=0.5)
        with self.assertRaises(ValueError):
            rr.binary_channel(p=0.5)  # missing q
        with self.assertRaises(ValueError):
            rr.binary_channel(p=0.5, q=0.5, epsilon=1.0)  # conflicting

    def test_invalid_channel_matrix(self):
        with self.assertRaises(ValueError):
            rr.validate_channel([[0.5, 0.4], [0.5, 0.5]])  # row sum != 1
        with self.assertRaises(ValueError):
            rr.validate_channel([[1.2, -0.2], [0.0, 1.0]])  # out of [0,1]
        with self.assertRaises(ValueError):
            rr.validate_channel([[1.0, 0.0]])  # not square
        with self.assertRaises(ValueError):
            rr.validate_channel([])

    def test_invalid_distribution(self):
        with self.assertRaises(ValueError):
            rr.validate_distribution([0.5, 0.4], 2)  # sum != 1
        with self.assertRaises(ValueError):
            rr.validate_distribution([1.2, -0.2], 2)  # negative
        with self.assertRaises(ValueError):
            rr.validate_distribution([1.0], 3)  # wrong length

    def test_invalid_counts(self):
        with self.assertRaises(ValueError):
            ChannelGroup("g", rr.kary_channel(2, 1.0), [5, -1])
        with self.assertRaises(ValueError):
            ChannelGroup("g", rr.kary_channel(2, 1.0), [5])  # wrong length

    def test_mismatched_group_sizes(self):
        g2 = ChannelGroup("a", rr.kary_channel(2, 1.0), [5, 5])
        g3 = ChannelGroup("b", rr.kary_channel(3, 1.0), [5, 5, 5])
        with self.assertRaises(ValueError):
            estimate_distribution([g2, g3])
        with self.assertRaises(ValueError):
            estimate_distribution([])


class TestChannelProperties(unittest.TestCase):
    def test_rows_sum_to_one(self):
        for k in (1, 2, 3, 7):
            P = rr.kary_channel(k, 1.3)
            for row in P:
                self.assertAlmostEqual(sum(row), 1.0, places=12)

    def test_k1_channel_is_constant(self):
        P = rr.kary_channel(1, 2.5)
        self.assertEqual(P, [[1.0]])
        rng = random.Random(0)
        counts = rr.simulate(rng, [1.0], P, 100)
        self.assertEqual(counts, [100])  # output is always the only symbol

    def test_epsilon0_output_independent_of_input(self):
        P = rr.kary_channel(4, 0.0)
        for row in P:
            self.assertEqual(row, [0.25] * 4)
        q1 = rr.output_probabilities(P, [1.0, 0.0, 0.0, 0.0])
        q2 = rr.output_probabilities(P, [0.0, 0.0, 0.0, 1.0])
        self.assertEqual(q1, q2)  # identical output law for any input law

    def test_large_epsilon_near_identity(self):
        P = rr.kary_channel(3, 20.0)
        self.assertAlmostEqual(P[0][0], 1.0, places=8)
        self.assertAlmostEqual(P[0][1], 0.0, places=8)

    def test_binary_epsilon_matches_kary(self):
        self.assertEqual(rr.binary_channel(epsilon=0.7), rr.kary_channel(2, 0.7))

    def test_binary_explicit_pq(self):
        P = rr.binary_channel(p=0.8, q=0.6)
        self.assertEqual(P, [[0.8, 0.19999999999999996], [0.4, 0.6]])


class TestSimulation(unittest.TestCase):
    def test_frequencies_close_to_theory(self):
        rng = random.Random(42)
        k, eps, n = 3, 1.0, 60000
        pi = [0.5, 0.3, 0.2]
        P = rr.kary_channel(k, eps)
        q = rr.output_probabilities(P, pi)
        counts = rr.simulate(rng, pi, P, n)
        self.assertEqual(sum(counts), n)
        for j in range(k):
            # 5-sigma multinomial tolerance
            tol = 5.0 * math.sqrt(q[j] * (1 - q[j]) / n)
            self.assertAlmostEqual(counts[j] / n, q[j], delta=tol)

    def test_epsilon0_simulation_independent_of_input(self):
        rng = random.Random(7)
        P = rr.kary_channel(3, 0.0)
        n = 30000
        c1 = rr.simulate(rng, [0.98, 0.01, 0.01], P, n)
        c2 = rr.simulate(rng, [0.01, 0.01, 0.98], P, n)
        for j in range(3):
            self.assertAlmostEqual(c1[j] / n, 1 / 3, delta=0.03)
            self.assertAlmostEqual(c2[j] / n, 1 / 3, delta=0.03)


class TestEstimation(unittest.TestCase):
    def test_single_group_recovers_distribution(self):
        rng = random.Random(123)
        pi = [0.4, 0.35, 0.25]
        P = rr.kary_channel(3, 1.5)
        counts = rr.simulate(rng, pi, P, 50000)
        res = estimate_distribution([ChannelGroup("g", P, counts)])
        self.assertTrue(res.identifiable)
        self.assertEqual(res.null_directions, [])
        for est, truth in zip(res.pi_constrained, pi):
            self.assertAlmostEqual(est, truth, delta=0.03)

    def test_constraints_nonnegative_and_sum_one(self):
        rng = random.Random(5)
        pi = [0.6, 0.3, 0.1]
        P = rr.kary_channel(3, 0.8)
        counts = rr.simulate(rng, pi, P, 2000)
        res = estimate_distribution([ChannelGroup("g", P, counts)])
        est = res.pi_constrained
        self.assertTrue(all(v >= 0.0 for v in est))
        self.assertAlmostEqual(sum(est), 1.0, places=9)

    def test_unconstrained_may_leave_simplex_and_clipped_separate(self):
        # Extreme counts force the inverse estimate outside [0, 1].
        P = rr.kary_channel(3, 1.0)
        res = estimate_distribution([ChannelGroup("g", P, [100, 0, 0])])
        self.assertTrue(any(v < 0.0 for v in res.pi_unconstrained))
        self.assertIsNotNone(res.pi_clipped)
        self.assertTrue(all(v >= 0.0 for v in res.pi_clipped))
        self.assertAlmostEqual(sum(res.pi_clipped), 1.0, places=12)
        # Clipped and constrained are reported as separate estimates.
        self.assertIsNot(res.pi_clipped, res.pi_constrained)

    def test_epsilon0_unidentifiable_no_inverse(self):
        P = rr.kary_channel(3, 0.0)
        res = estimate_distribution([ChannelGroup("g", P, [100, 100, 100])])
        self.assertFalse(res.identifiable)
        self.assertIsNone(res.pi_unconstrained)
        self.assertIsNone(res.pi_constrained)
        self.assertIsNone(res.ci95)
        self.assertEqual(res.condition_number, float("inf"))
        # Null space of a rank-1 3x3 design has dimension 2.
        self.assertEqual(len(res.null_directions), 2)
        for d in res.null_directions:
            self.assertAlmostEqual(sum(d), 0.0, places=9)  # sum-zero dirs

    def test_heterogeneous_groups_joint_recovery(self):
        rng = random.Random(99)
        pi = [0.5, 0.3, 0.2]
        P1 = rr.kary_channel(3, 0.4)
        P2 = rr.kary_channel(3, 2.5)
        g1 = ChannelGroup("weak", P1, rr.simulate(rng, pi, P1, 40000))
        g2 = ChannelGroup("strong", P2, rr.simulate(rng, pi, P2, 40000))
        res = estimate_distribution([g1, g2])
        self.assertTrue(res.identifiable)
        for est, truth in zip(res.pi_constrained, pi):
            self.assertAlmostEqual(est, truth, delta=0.03)
        # Joint condition number is finite and smaller than the weak
        # channel's alone would suggest (information pooling).
        self.assertLess(res.condition_number, 100.0)

    def test_residuals_small_on_synthetic_data(self):
        rng = random.Random(11)
        pi = [0.3, 0.7]
        P = rr.binary_channel(epsilon=1.2)
        counts = rr.simulate(rng, pi, P, 80000)
        res = estimate_distribution([ChannelGroup("bin", P, counts)])
        for r in res.residuals["bin"]:
            self.assertAlmostEqual(r, 0.0, delta=0.02)

    def test_condition_number_finite_for_positive_epsilon(self):
        P = rr.kary_channel(3, 1.0)
        res = estimate_distribution([ChannelGroup("g", P, [10, 10, 10])])
        self.assertTrue(math.isfinite(res.condition_number))
        self.assertGreater(res.condition_number, 1.0)

    def test_k1_trivial_estimation(self):
        P = rr.kary_channel(1, 0.0)  # epsilon irrelevant when k == 1
        res = estimate_distribution([ChannelGroup("g", P, [250])])
        self.assertTrue(res.identifiable)
        self.assertAlmostEqual(res.pi_constrained[0], 1.0, places=12)
        self.assertAlmostEqual(res.pi_unconstrained[0], 1.0, places=12)

    def test_project_simplex(self):
        v = project_simplex([1.2, -0.5, 0.3])
        self.assertTrue(all(x >= 0.0 for x in v))
        self.assertAlmostEqual(sum(v), 1.0, places=12)


class TestExactReference(unittest.TestCase):
    def test_total_mass_and_zero_bias(self):
        P = rr.binary_channel(epsilon=1.0)
        pi = [0.25, 0.75]
        m = exact.exact_estimator_moments(P, pi, 12)
        self.assertAlmostEqual(m["total_mass"], 1.0, places=10)
        for b in m["bias"]:
            self.assertAlmostEqual(b, 0.0, places=10)  # linear => unbiased

    def test_exact_variance_matches_asymptotic_formula(self):
        P = rr.kary_channel(3, 1.2)
        pi = [0.5, 0.3, 0.2]
        n = 9
        m = exact.exact_estimator_moments(P, pi, n)
        # Asymptotic covariance with the TRUE pi plugged in (not pi_hat).
        pt = linalg.transpose(P)
        q = rr.output_probabilities(P, pi)
        cov_f = [[(q[i] if i == j else 0.0) - q[i] * q[j]
                  for j in range(3)] for i in range(3)]
        pt_inv = linalg.inverse(pt)
        cov = linalg.matmul(linalg.matmul(pt_inv, cov_f),
                            linalg.transpose(pt_inv))
        for i in range(3):
            self.assertAlmostEqual(m["variance"][i], cov[i][i] / n, places=10)

    def test_multinomial_pmf_sums_to_one(self):
        q = [0.2, 0.3, 0.5]
        total = sum(exact.multinomial_pmf(c, q)
                    for c in exact.compositions(7, 3))
        self.assertAlmostEqual(total, 1.0, places=12)


class TestConfidenceIntervals(unittest.TestCase):
    def test_ci_present_and_shrinks_with_n(self):
        rng = random.Random(2024)
        pi = [0.4, 0.6]
        P = rr.binary_channel(epsilon=1.0)
        small = estimate_distribution(
            [ChannelGroup("g", P, rr.simulate(rng, pi, P, 1000))])
        large = estimate_distribution(
            [ChannelGroup("g", P, rr.simulate(rng, pi, P, 100000))])
        self.assertIsNotNone(small.ci95)
        w_small = small.ci95[0][1] - small.ci95[0][0]
        w_large = large.ci95[0][1] - large.ci95[0][0]
        self.assertLess(w_large, w_small / 5.0)  # ~ sqrt(100) narrower

    def test_ci_unavailable_for_degenerate_channel(self):
        P = rr.kary_channel(2, 0.0)
        res = estimate_distribution([ChannelGroup("g", P, [50, 50])])
        self.assertIsNone(res.ci95)
        self.assertTrue(any("NOT identifiable" in n for n in res.notes))

    def test_notes_document_assumptions(self):
        P = rr.binary_channel(epsilon=1.0)
        res = estimate_distribution([ChannelGroup("g", P, [40, 60])])
        joined = " ".join(res.notes)
        self.assertIn("i.i.d.", joined)
        self.assertIn("normal approximation", joined)


if __name__ == "__main__":
    unittest.main()
