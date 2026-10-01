"""Exact (enumeration) and empirical (Monte-Carlo) verification tests."""

import math
import random
import unittest

from stratified_sampling import (
    IncrementalStatistics,
    draw_stratified_sample,
    empirical_stratified_distribution,
    exact_stratified_distribution,
    exact_two_stage_distribution,
    iter_stratified_designs,
    theoretical_stratified_variance,
)

STRATIFIED_POPULATION = {
    "urban": [1.0, 2.0, 3.0, 4.0, 5.0],
    "rural": [10.0, 20.0, 30.0],
}
STRATIFIED_SIZES = {"urban": 2, "rural": 2}

CLUSTER_POPULATION = {
    "north": [[1.0, 1.0, 2.0], [5.0, 5.0, 6.0], [9.0, 9.0, 10.0]],
    "south": [[2.0, 3.0, 4.0], [8.0, 9.0, 10.0]],
}
FIRST_STAGE = {"north": 2, "south": 2}
SECOND_STAGE = {"north": 2, "south": 2}


class TestExactEnumeration(unittest.TestCase):
    def test_design_count(self):
        designs = list(
            iter_stratified_designs(STRATIFIED_POPULATION, STRATIFIED_SIZES)
        )
        # C(5,2) * C(3,2) = 10 * 3 = 30 designs.
        self.assertEqual(len(designs), 30)

    def test_exact_unbiasedness_and_variance(self):
        summary = exact_stratified_distribution(
            STRATIFIED_POPULATION, STRATIFIED_SIZES
        )
        self.assertEqual(summary.kind, "exact")
        self.assertTrue(summary.unbiased)
        self.assertTrue(summary.variance_matches_theory)
        self.assertTrue(summary.variance_estimator_unbiased)
        self.assertAlmostEqual(summary.expectation, summary.true_mean, places=9)
        self.assertAlmostEqual(
            summary.variance, summary.theoretical_variance, places=9
        )

    def test_exact_two_stage_unbiasedness_and_variance(self):
        summary = exact_two_stage_distribution(
            CLUSTER_POPULATION, FIRST_STAGE, SECOND_STAGE
        )
        self.assertEqual(summary.kind, "exact")
        # north: C(3,2)*C(3,2)^2 = 27 ; south: C(2,2)*C(3,2)^2 = 9 -> 243.
        self.assertEqual(summary.num_designs, 243)
        self.assertTrue(summary.unbiased)
        self.assertTrue(summary.variance_matches_theory)
        self.assertTrue(summary.variance_estimator_unbiased)

    def test_two_stage_variance_exceeds_naive_element_formula(self):
        # Cluster members are correlated; treating them as independent
        # (naive element-level SRS formula) would understate variance.
        summary = exact_two_stage_distribution(
            CLUSTER_POPULATION, FIRST_STAGE, SECOND_STAGE
        )
        all_values = [
            y
            for clusters in CLUSTER_POPULATION.values()
            for cluster in clusters
            for y in cluster
        ]
        stats = IncrementalStatistics(all_values)
        n_total = sum(FIRST_STAGE[h] * SECOND_STAGE[h] for h in FIRST_STAGE)
        N = len(all_values)
        naive = (1 - n_total / N) * stats.sample_variance / n_total
        self.assertGreater(summary.variance, naive)


class TestEmpiricalVerification(unittest.TestCase):
    def test_empirical_matches_exact_within_tolerance(self):
        rng = random.Random(20261001)
        empirical = empirical_stratified_distribution(
            STRATIFIED_POPULATION,
            STRATIFIED_SIZES,
            replications=20000,
            rng=rng,
        )
        exact = exact_stratified_distribution(
            STRATIFIED_POPULATION, STRATIFIED_SIZES
        )
        self.assertEqual(empirical.kind, "empirical")
        self.assertEqual(exact.kind, "exact")
        self.assertTrue(empirical.unbiased)
        self.assertTrue(empirical.variance_matches_theory)
        self.assertAlmostEqual(
            empirical.expectation, exact.expectation, delta=0.05
        )
        self.assertAlmostEqual(
            empirical.variance,
            exact.variance,
            delta=0.15 * exact.variance + 1e-9,
        )


class TestSamplingRecord(unittest.TestCase):
    def test_seed_and_sizes_recorded(self):
        rng = random.Random(7)
        samples, record = draw_stratified_sample(
            STRATIFIED_POPULATION, STRATIFIED_SIZES, rng=rng, seed=7
        )
        self.assertEqual(record.seed, 7)
        self.assertEqual(dict(record.stage1_sizes), {"urban": 2, "rural": 2})
        for sample in samples:
            self.assertEqual(len(sample.values), STRATIFIED_SIZES[sample.name])

    def test_same_seed_reproduces_sample(self):
        first, _ = draw_stratified_sample(
            STRATIFIED_POPULATION,
            STRATIFIED_SIZES,
            rng=random.Random(42),
            seed=42,
        )
        second, _ = draw_stratified_sample(
            STRATIFIED_POPULATION,
            STRATIFIED_SIZES,
            rng=random.Random(42),
            seed=42,
        )
        self.assertEqual([s.values for s in first], [s.values for s in second])

    def test_theoretical_variance_helper(self):
        value = theoretical_stratified_variance(
            STRATIFIED_POPULATION, STRATIFIED_SIZES
        )
        self.assertTrue(math.isfinite(value))
        self.assertGreater(value, 0.0)


if __name__ == "__main__":
    unittest.main()
