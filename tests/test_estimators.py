"""Tests for the core estimators.

Exact conclusions (enumeration over all possible samples, closed-form
references) and empirical conclusions (Monte Carlo averages) are kept in
separate test classes and labeled as such.
"""

import itertools
import math
import random
import unittest

from importance_sampling import (
    DiscreteDistribution,
    effective_sample_size,
    estimate,
    exact_expectation,
    ordinary_is,
    self_normalized_is,
)

TARGET = DiscreteDistribution({0: 0.5, 1: 0.3, 2: 0.2}, name="target")
PROPOSAL = DiscreteDistribution({0: 0.2, 1: 0.3, 2: 0.5}, name="proposal")


def f(x):
    return float(x)


def expected_over_all_samples(estimator, n):
    """EXACT: E[estimator] by enumerating every possible sample of size n."""
    total = 0.0
    for xs in itertools.product([0, 1, 2], repeat=n):
        prob = 1.0
        for x in xs:
            prob *= PROPOSAL.pdf(x)
        logw = [TARGET.log_pdf(x) - PROPOSAL.log_pdf(x) for x in xs]
        total += prob * estimator(logw, [f(x) for x in xs])
    return total


class TestExactReference(unittest.TestCase):
    """EXACT conclusions from closed-form sums over the finite domain."""

    def test_exact_expectation(self):
        self.assertAlmostEqual(exact_expectation(TARGET, f), 0.7)

    def test_ordinary_is_is_unbiased(self):
        # EXACT: enumerating all 3^2 samples, E[ordinary IS] == E_p[f].
        mu_hat = expected_over_all_samples(ordinary_is, n=2)
        self.assertAlmostEqual(mu_hat, exact_expectation(TARGET, f), places=12)

    def test_self_normalized_is_has_finite_n_bias_that_shrinks(self):
        # EXACT: E[SNIS] != E_p[f] for finite n (bias exists), and the
        # bias at n=2 is smaller than at n=1 (it vanishes as O(1/n)).
        mu = exact_expectation(TARGET, f)
        bias_n1 = abs(expected_over_all_samples(self_normalized_is, n=1) - mu)
        bias_n2 = abs(expected_over_all_samples(self_normalized_is, n=2) - mu)
        self.assertGreater(bias_n1, 1e-12)
        self.assertGreater(bias_n2, 1e-12)
        self.assertLess(bias_n2, bias_n1)


class TestLogStabilization(unittest.TestCase):
    def test_huge_log_weights_do_not_overflow(self):
        logw = [700.0, 700.0, 700.0]
        self.assertAlmostEqual(ordinary_is(logw, [1.0, 1.0, 1.0]) / math.exp(700), 1.0, places=12)
        self.assertAlmostEqual(self_normalized_is(logw, [1.0, 2.0, 3.0]), 2.0)

    def test_extreme_log_weight_spread(self):
        logw = [1000.0, 999.0, 998.0]
        # Stable reference computed with an explicit shift.
        ws = [math.exp(lw - 1000.0) for lw in logw]
        expected = sum(w * v for w, v in zip(ws, [1.0, 2.0, 3.0])) / sum(ws)
        self.assertAlmostEqual(self_normalized_is(logw, [1.0, 2.0, 3.0]), expected)


class TestEffectiveSampleSize(unittest.TestCase):
    def test_equal_weights_give_ess_equal_to_n(self):
        self.assertEqual(effective_sample_size([0.3] * 7), 7.0)
        self.assertEqual(effective_sample_size([-2.5] * 4), 4.0)

    def test_single_dominant_weight(self):
        ess = effective_sample_size([0.0, -30.0, -30.0, -30.0])
        self.assertAlmostEqual(ess, 1.0, places=6)


class TestEmpiricalBehavior(unittest.TestCase):
    """EMPIRICAL conclusions from Monte Carlo replication (seeded)."""

    def test_ordinary_is_empirically_centers_on_exact(self):
        rng = random.Random(20240901)
        estimates = [estimate(TARGET, PROPOSAL, f, 50, rng).ordinary for _ in range(400)]
        mean_estimate = sum(estimates) / len(estimates)
        self.assertAlmostEqual(mean_estimate, exact_expectation(TARGET, f), delta=0.03)

    def test_self_normalized_is_empirically_converges(self):
        rng = random.Random(777)
        result = estimate(TARGET, PROPOSAL, f, 20000, rng)
        self.assertAlmostEqual(result.self_normalized, exact_expectation(TARGET, f), delta=0.02)

    def test_seed_and_n_are_recorded(self):
        rng = random.Random(1)
        result = estimate(TARGET, PROPOSAL, f, 123, rng, seed=42)
        self.assertEqual(result.seed, 42)
        self.assertEqual(result.n, 123)
        self.assertTrue(result.available)
        self.assertEqual(result.errors, [])

    def test_reproducibility_from_injected_rng(self):
        first = estimate(TARGET, PROPOSAL, f, 100, random.Random(99))
        second = estimate(TARGET, PROPOSAL, f, 100, random.Random(99))
        self.assertEqual(first.ordinary, second.ordinary)
        self.assertEqual(first.self_normalized, second.self_normalized)


if __name__ == "__main__":
    unittest.main()
