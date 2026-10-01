"""Tests for the two-stage stratified cluster estimator."""

import unittest

from stratified_sampling import (
    ClusterSample,
    ClusterStratumSample,
    MissingStratumError,
    StratifiedSamplingError,
    ZeroInclusionProbabilityError,
    estimate_two_stage_mean,
)


def simple_stratum():
    # M = 4 clusters, m = 2 sampled, each N = 10 with n = 5 observed.
    return ClusterStratumSample(
        "h1",
        population_clusters=4,
        clusters=[
            ClusterSample(10, [1.0, 2.0, 3.0, 4.0, 5.0]),
            ClusterSample(10, [5.0, 6.0, 7.0, 8.0, 9.0]),
        ],
    )


class TestTwoStageEstimator(unittest.TestCase):
    def test_hand_computed_total_and_mean(self):
        result = estimate_two_stage_mean([simple_stratum()], total_population=40)
        # t_hat = (4/2) * (30 + 70) = 200 ; mean = 200 / 40 = 5
        self.assertAlmostEqual(result.total, 200.0, places=9)
        self.assertAlmostEqual(result.mean, 5.0, places=9)
        self.assertEqual(result.mean_denominator, "known")
        self.assertFalse(result.mean_variance_approximate)

    def test_hand_computed_variance_components(self):
        result = estimate_two_stage_mean([simple_stratum()], total_population=40)
        diag = result.strata[0]
        # Between: M^2 (1 - m/M) s_t^2 / m with s_t^2 = var(30, 70) = 800.
        between = 16 * (1 - 2 / 4) * 800 / 2
        # Within: (M/m) * sum_i N_i^2 (1 - n/N) s_i^2 / n with s_i^2 = 2.5.
        within = 2 * (100 * 0.5 * 2.5 / 5 + 100 * 0.5 * 2.5 / 5)
        self.assertAlmostEqual(diag.between_component, between, places=9)
        self.assertAlmostEqual(diag.within_component, within, places=9)
        self.assertAlmostEqual(
            result.variance_mean, (between + within) / 40**2, places=12
        )
        self.assertTrue(result.variance_estimable)

    def test_inclusion_probabilities_recorded(self):
        result = estimate_two_stage_mean([simple_stratum()], total_population=40)
        self.assertAlmostEqual(
            result.strata[0].first_stage_inclusion_probability, 0.5, places=12
        )

    def test_single_cluster_stratum_between_not_estimable(self):
        stratum = ClusterStratumSample(
            "h", 5, [ClusterSample(4, [1.0, 2.0, 3.0])]
        )
        result = estimate_two_stage_mean([stratum], total_population=20)
        self.assertIsNone(result.strata[0].between_component)
        self.assertFalse(result.variance_estimable)
        self.assertIsNone(result.variance_mean)
        self.assertIsNotNone(result.mean)

    def test_single_element_cluster_within_not_estimable(self):
        stratum = ClusterStratumSample(
            "h",
            2,
            [ClusterSample(9, [3.0]), ClusterSample(9, [5.0])],
        )
        result = estimate_two_stage_mean([stratum], total_population=18)
        self.assertIsNone(result.strata[0].within_component)
        self.assertFalse(result.variance_estimable)

    def test_full_first_stage_has_zero_between_component(self):
        stratum = ClusterStratumSample(
            "h",
            2,
            [ClusterSample(3, [1.0, 2.0]), ClusterSample(3, [4.0, 5.0])],
        )
        result = estimate_two_stage_mean([stratum], total_population=6)
        self.assertEqual(result.strata[0].between_component, 0.0)

    def test_fully_enumerated_design_has_zero_variance(self):
        stratum = ClusterStratumSample(
            "h",
            2,
            [ClusterSample(2, [1.0, 2.0]), ClusterSample(2, [3.0, 4.0])],
        )
        result = estimate_two_stage_mean([stratum], total_population=4)
        self.assertTrue(result.strata[0].fully_enumerated)
        self.assertEqual(result.variance_mean, 0.0)
        self.assertAlmostEqual(result.mean, 2.5, places=12)

    def test_missing_stratum_rejected(self):
        with self.assertRaises(MissingStratumError):
            ClusterStratumSample("ghost", 3, [])

    def test_zero_second_stage_inclusion_probability_rejected(self):
        with self.assertRaises(ZeroInclusionProbabilityError):
            ClusterSample(10, [])

    def test_invalid_sizes_rejected(self):
        with self.assertRaises(StratifiedSamplingError):
            ClusterSample(2, [1.0, 2.0, 3.0])
        with self.assertRaises(StratifiedSamplingError):
            ClusterStratumSample(
                "h", 1, [ClusterSample(2, [1.0]), ClusterSample(2, [2.0])]
            )

    def test_estimated_denominator_flagged_approximate(self):
        result = estimate_two_stage_mean([simple_stratum()])
        self.assertEqual(result.mean_denominator, "estimated")
        self.assertTrue(result.mean_variance_approximate)

    def test_cluster_members_not_treated_as_independent(self):
        # Perfectly correlated clusters (constant within): a naive
        # element-level analysis would report ~0 variance; the design
        # estimator keeps the between-cluster component.
        stratum = ClusterStratumSample(
            "h",
            4,
            [ClusterSample(5, [2.0] * 5), ClusterSample(5, [8.0] * 5)],
        )
        result = estimate_two_stage_mean([stratum], total_population=20)
        diag = result.strata[0]
        self.assertEqual(diag.within_component, 0.0)
        self.assertGreater(diag.between_component, 0.0)
        self.assertGreater(result.variance_mean, 0.0)


if __name__ == "__main__":
    unittest.main()
