"""Tests for the one-stage stratified estimator."""

import math
import unittest

from stratified_sampling import (
    MissingStratumError,
    StratifiedSamplingError,
    StratumSample,
    estimate_stratified_mean,
)


class TestStratifiedMean(unittest.TestCase):
    def test_unequal_strata_are_weighted_not_averaged(self):
        # Stratum sizes differ: the naive mean of stratum means (2.5) is wrong.
        result = estimate_stratified_mean([
            StratumSample("large", 800, [1.0, 2.0]),
            StratumSample("small", 200, [3.0, 4.0]),
        ])
        naive = (1.5 + 3.5) / 2
        expected = 0.8 * 1.5 + 0.2 * 3.5
        self.assertAlmostEqual(result.mean, expected, places=12)
        self.assertNotAlmostEqual(result.mean, naive, places=6)

    def test_weights_sum_to_one(self):
        result = estimate_stratified_mean([
            StratumSample("a", 700, [1.0, 2.0, 3.0]),
            StratumSample("b", 200, [4.0, 5.0]),
            StratumSample("c", 100, [6.0, 7.0]),
        ])
        total = math.fsum(d.weight for d in result.strata)
        self.assertAlmostEqual(total, 1.0, places=12)

    def test_fully_sampled_stratum_contributes_zero_variance(self):
        result = estimate_stratified_mean([
            StratumSample("census", 3, [1.0, 2.0, 3.0]),
            StratumSample("sampled", 100, [10.0, 20.0, 30.0]),
        ])
        census = result.strata[0]
        self.assertTrue(census.fully_sampled)
        self.assertEqual(census.variance_contribution, 0.0)
        self.assertTrue(census.variance_estimable)
        sampled = result.strata[1]
        expected = sampled.weight**2 * (1 - 3 / 100) * sampled.sample_variance / 3
        self.assertAlmostEqual(result.variance, expected, places=12)

    def test_single_observation_census_stratum_is_exact(self):
        # n_h == N_h == 1: fpc == 0, contribution exactly 0, not "unknown".
        result = estimate_stratified_mean([
            StratumSample("singleton_census", 1, [42.0]),
            StratumSample("sampled", 10, [1.0, 2.0, 4.0]),
        ])
        self.assertTrue(result.variance_estimable)
        self.assertEqual(result.strata[0].variance_contribution, 0.0)

    def test_single_sample_stratum_variance_not_estimable(self):
        # n_h == 1 but N_h > 1: must not pretend to have an exact variance.
        result = estimate_stratified_mean([
            StratumSample("thin", 500, [7.0]),
            StratumSample("ok", 500, [1.0, 2.0, 3.0]),
        ])
        thin = result.strata[0]
        self.assertIsNone(thin.sample_variance)
        self.assertIsNone(thin.variance_contribution)
        self.assertFalse(thin.variance_estimable)
        self.assertFalse(result.variance_estimable)
        self.assertIsNone(result.variance)
        self.assertIsNone(result.standard_error)
        self.assertAlmostEqual(result.mean, 0.5 * 7.0 + 0.5 * 2.0, places=12)

    def test_missing_stratum_rejected(self):
        with self.assertRaises(MissingStratumError):
            StratumSample("ghost", 100, [])

    def test_sample_larger_than_population_rejected(self):
        with self.assertRaises(StratifiedSamplingError):
            StratumSample("bad", 2, [1.0, 2.0, 3.0])

    def test_known_variance_value(self):
        # Hand-computed: W=(0.8, 0.2), s^2 = 0.5 in both strata, n = 2.
        result = estimate_stratified_mean([
            StratumSample("a", 800, [1.0, 2.0]),
            StratumSample("b", 200, [3.0, 4.0]),
        ])
        expected = (0.8**2 * (1 - 2 / 800) * 0.5 / 2
                    + 0.2**2 * (1 - 2 / 200) * 0.5 / 2)
        self.assertAlmostEqual(result.variance, expected, places=12)
        self.assertAlmostEqual(
            result.standard_error, math.sqrt(expected), places=12
        )


if __name__ == "__main__":
    unittest.main()
