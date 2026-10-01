import random
import unittest

from randomized_response import (
    RandomizedResponseChannel,
    estimate_from_counts,
    exact_binary_reference,
    simulate_counts,
    theoretical_output_distribution,
)


class TestFrequenciesVsTheory(unittest.TestCase):
    def test_simulated_frequencies_approach_theoretical_probabilities(self):
        rng = random.Random(123)
        for k, eps, true in (
            (2, 1.0, [0.3, 0.7]),
            (4, 0.5, [0.1, 0.2, 0.3, 0.4]),
            (6, 2.0, [0.05, 0.1, 0.15, 0.2, 0.25, 0.25]),
        ):
            ch = RandomizedResponseChannel(k=k, epsilon=eps)
            n = 400_000
            counts = simulate_counts(ch, true, n, rng)
            theo = theoretical_output_distribution(ch, true)
            for c, p in zip(counts, theo):
                self.assertAlmostEqual(c / n, p, delta=0.01)

    def test_epsilon0_frequencies_uniform_regardless_of_input(self):
        ch = RandomizedResponseChannel(k=4, epsilon=0.0)
        counts = simulate_counts(ch, [0.9, 0.05, 0.03, 0.02], 200_000, random.Random(5))
        for c in counts:
            self.assertAlmostEqual(c / 200_000, 0.25, delta=0.01)


class TestExactBinaryReference(unittest.TestCase):
    def test_raw_estimator_is_exactly_unbiased(self):
        for eps, x0, n in ((1.0, 0.3, 25), (0.5, 0.9, 40), (2.0, 0.5, 10)):
            ref = exact_binary_reference(eps, x0, n)
            self.assertAlmostEqual(ref.mean_raw, x0, places=12)
            self.assertAlmostEqual(sum(ref.probabilities), 1.0, places=12)

    def test_exact_variance_matches_binomial_formula(self):
        eps, x0, n = 1.0, 0.4, 30
        ref = exact_binary_reference(eps, x0, n)
        ch = RandomizedResponseChannel(k=2, epsilon=eps)
        p = ch.p_stay
        s = (2 * p - 1) * x0 + (1 - p)
        expected_var = s * (1 - s) / (n * (2 * p - 1) ** 2)
        self.assertAlmostEqual(ref.var_raw, expected_var, places=15)

    def test_clipped_estimator_bias_is_exact_and_shrinks_with_n(self):
        # Boundary truth makes clipping biased; the bias must vanish as n grows.
        eps, x0 = 0.8, 0.97
        bias_small = abs(exact_binary_reference(eps, x0, 20).bias_clipped)
        bias_large = abs(exact_binary_reference(eps, x0, 500).bias_clipped)
        self.assertGreater(bias_small, 0.0)
        self.assertLess(bias_large, bias_small)

    def test_simulation_matches_exact_reference(self):
        # Monte-Carlo mean of the raw estimator vs the exact reference mean.
        eps, x0, n = 1.0, 0.35, 15
        ref = exact_binary_reference(eps, x0, n)
        ch = RandomizedResponseChannel(k=2, epsilon=eps)
        rng = random.Random(99)
        trials = 4_000
        total = 0.0
        for _ in range(trials):
            counts = simulate_counts(ch, [x0, 1.0 - x0], n, rng)
            total += estimate_from_counts(ch, counts).raw_estimate[0]
        self.assertAlmostEqual(total / trials, ref.mean_raw, delta=0.02)

    def test_epsilon0_reference_rejected(self):
        with self.assertRaises(ValueError):
            exact_binary_reference(0.0, 0.5, 10)


if __name__ == "__main__":
    unittest.main()
