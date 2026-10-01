"""Tests for incremental add / retract statistics."""

import statistics
import unittest

from stratified_sampling import IncrementalStatistics


class TestIncrementalStatistics(unittest.TestCase):
    def test_matches_batch_statistics(self):
        values = [3.5, -1.0, 7.25, 0.0, 4.4, 9.1]
        stats = IncrementalStatistics(values)
        self.assertEqual(stats.count, len(values))
        self.assertAlmostEqual(stats.mean, statistics.fmean(values), places=12)
        self.assertAlmostEqual(
            stats.sample_variance, statistics.variance(values), places=12
        )
        self.assertAlmostEqual(
            stats.population_variance, statistics.pvariance(values), places=12
        )

    def test_retract_restores_previous_state(self):
        stats = IncrementalStatistics([1.0, 2.0])
        mean_before = stats.mean
        m2_before = stats.sum_of_squared_deviations
        stats.add(99.0)
        stats.retract(99.0)
        self.assertEqual(stats.count, 2)
        self.assertAlmostEqual(stats.mean, mean_before, places=12)
        self.assertAlmostEqual(
            stats.sum_of_squared_deviations, m2_before, places=12
        )

    def test_sample_identity_change_is_incremental(self):
        # Replacing one observation must equal recomputing the new sample.
        stats = IncrementalStatistics([10.0, 20.0, 30.0, 40.0])
        stats.retract(20.0)
        stats.add(25.0)
        new_sample = [10.0, 30.0, 40.0, 25.0]
        self.assertAlmostEqual(
            stats.mean, statistics.fmean(new_sample), places=10
        )
        self.assertAlmostEqual(
            stats.sample_variance, statistics.variance(new_sample), places=10
        )

    def test_retract_down_to_empty(self):
        stats = IncrementalStatistics([5.0, 5.0, 5.0])
        for _ in range(3):
            stats.retract(5.0)
        self.assertEqual(stats.count, 0)
        self.assertIsNone(stats.sample_variance)
        with self.assertRaises(ValueError):
            _ = stats.mean

    def test_retract_from_empty_rejected(self):
        with self.assertRaises(ValueError):
            IncrementalStatistics().retract(1.0)

    def test_single_observation_has_no_sample_variance(self):
        stats = IncrementalStatistics([3.0])
        self.assertIsNone(stats.sample_variance)
        self.assertEqual(stats.population_variance, 0.0)


if __name__ == "__main__":
    unittest.main()
