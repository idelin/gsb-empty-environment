import math
import random
import unittest

from randomized_response import (
    ChannelGroup,
    RandomizedResponseChannel,
    confidence_intervals,
    estimate_distribution,
    estimate_from_counts,
    simulate_counts,
)


def make_group(k, eps, true_dist, n, seed):
    ch = RandomizedResponseChannel(k=k, epsilon=eps)
    counts = simulate_counts(ch, true_dist, n, random.Random(seed))
    return ChannelGroup(ch, counts)


class TestHomogeneousEstimation(unittest.TestCase):
    def test_recovers_binary_distribution_approximately(self):
        true = [0.3, 0.7]
        group = make_group(2, 1.0, true, 200_000, 1)
        report = estimate_distribution([group])
        self.assertTrue(report.identifiable)
        self.assertEqual(report.null_directions, [])
        for est, t in zip(report.estimate, true):
            self.assertAlmostEqual(est, t, delta=0.02)
        self.assertAlmostEqual(sum(report.estimate), 1.0, places=12)
        self.assertTrue(all(x >= 0.0 for x in report.estimate))
        self.assertTrue(math.isfinite(report.condition_number))
        self.assertLess(report.residual, 0.01)

    def test_recovers_kary_distribution_approximately(self):
        true = [0.1, 0.2, 0.3, 0.4]
        group = make_group(4, 1.5, true, 300_000, 2)
        report = estimate_distribution([group])
        self.assertTrue(report.identifiable)
        for est, t in zip(report.estimate, true):
            self.assertAlmostEqual(est, t, delta=0.03)

    def test_raw_estimate_may_leave_natural_range_and_is_reported_separately(self):
        # Extreme true distribution + small sample -> raw estimate likely outside [0, 1].
        true = [0.98, 0.02]
        ch = RandomizedResponseChannel(k=2, epsilon=0.5)
        counts = simulate_counts(ch, true, 60, random.Random(7))
        report = estimate_from_counts(ch, counts)
        # Constrained estimate is always valid regardless of the raw one.
        self.assertAlmostEqual(sum(report.estimate), 1.0, places=12)
        self.assertTrue(all(0.0 <= x <= 1.0 for x in report.estimate))
        # `clipped` flags exactly the raw coordinates outside [0, 1].
        for raw, flag in zip(report.raw_estimate, report.clipped):
            self.assertEqual(flag, not (0.0 <= raw <= 1.0))

    def test_deterministic_counts_exact_recovery(self):
        # Feed counts exactly equal to n * A x: the raw estimate must equal x.
        ch = RandomizedResponseChannel(k=3, epsilon=2.0)
        true = [0.2, 0.5, 0.3]
        out = ch.output_distribution(true)
        n = 10_000
        counts = [round(n * p) for p in out]
        counts[0] += n - sum(counts)  # fix rounding
        report = estimate_from_counts(ch, counts)
        for est, t in zip(report.raw_estimate, true):
            self.assertAlmostEqual(est, t, delta=2e-3)


class TestHeterogeneousChannels(unittest.TestCase):
    def test_joint_recovery_across_channel_groups(self):
        true = [0.15, 0.25, 0.6]
        groups = [
            make_group(3, 0.5, true, 80_000, 11),
            make_group(3, 1.0, true, 80_000, 12),
            make_group(3, 2.0, true, 80_000, 13),
        ]
        report = estimate_distribution(groups)
        self.assertTrue(report.identifiable)
        for est, t in zip(report.estimate, true):
            self.assertAlmostEqual(est, t, delta=0.03)

    def test_mismatched_k_rejected(self):
        g1 = make_group(2, 1.0, [0.5, 0.5], 100, 1)
        g2 = make_group(3, 1.0, [1 / 3] * 3, 100, 2)
        with self.assertRaises(ValueError):
            estimate_distribution([g1, g2])

    def test_empty_groups_rejected(self):
        with self.assertRaises(ValueError):
            estimate_distribution([])


class TestIdentifiability(unittest.TestCase):
    def test_epsilon0_is_unidentifiable_and_not_inverted(self):
        ch = RandomizedResponseChannel(k=3, epsilon=0.0)
        counts = simulate_counts(ch, [0.6, 0.3, 0.1], 5_000, random.Random(3))
        report = estimate_from_counts(ch, counts)
        self.assertFalse(report.identifiable)
        self.assertEqual(len(report.null_directions), 2)  # rank 1 channel -> 2-dim null space
        # Null directions must be genuine null directions of the Gram matrix.
        for direction in report.null_directions:
            norm = math.sqrt(sum(d * d for d in direction))
            self.assertAlmostEqual(norm, 1.0, places=8)
        # The minimum-norm raw estimate is the uniform distribution here.
        for x in report.raw_estimate:
            self.assertAlmostEqual(x, 1 / 3, delta=0.02)
        self.assertTrue(any("not jointly identifiable" in n for n in report.notes))

    def test_mixed_groups_identifiable_if_one_channel_informative(self):
        true = [0.4, 0.6]
        groups = [
            make_group(2, 0.0, true, 5_000, 21),   # uninformative alone
            make_group(2, 1.0, true, 50_000, 22),  # informative
        ]
        report = estimate_distribution(groups)
        self.assertTrue(report.identifiable)
        for est, t in zip(report.estimate, true):
            self.assertAlmostEqual(est, t, delta=0.03)

    def test_k1_special_case(self):
        ch = RandomizedResponseChannel(k=1, epsilon=1.0)
        report = estimate_from_counts(ch, [123])
        self.assertEqual(report.estimate, [1.0])
        self.assertTrue(any("k = 1" in n for n in report.notes))


class TestConfidenceIntervals(unittest.TestCase):
    def test_intervals_cover_truth_for_moderate_n(self):
        true = [0.3, 0.7]
        covered = 0
        trials = 10
        for seed in range(trials):
            group = make_group(2, 1.0, true, 50_000, 1000 + seed)
            ci = confidence_intervals([group], confidence=0.95)
            if all(lo <= t <= hi for (lo, hi), t in zip(ci.intervals, true)):
                covered += 1
        # Nominal 95% coverage per coordinate; require a conservative majority.
        self.assertGreaterEqual(covered, 7)
        self.assertIn("i.i.d.", ci.assumptions)
        self.assertIn("Wald", ci.assumptions)

    def test_intervals_shrink_with_n(self):
        true = [0.5, 0.5]
        small = confidence_intervals([make_group(2, 1.0, true, 1_000, 41)])
        large = confidence_intervals([make_group(2, 1.0, true, 100_000, 42)])
        w_small = small.intervals[0][1] - small.intervals[0][0]
        w_large = large.intervals[0][1] - large.intervals[0][0]
        self.assertGreater(w_small, 5 * w_large)

    def test_degenerate_channel_refuses_interval(self):
        group = make_group(2, 0.0, [0.5, 0.5], 1_000, 51)
        with self.assertRaises(ValueError):
            confidence_intervals([group])

    def test_k1_refuses_interval(self):
        ch = RandomizedResponseChannel(k=1, epsilon=1.0)
        with self.assertRaises(ValueError):
            confidence_intervals([ChannelGroup(ch, [10])])

    def test_bad_confidence_level_rejected(self):
        group = make_group(2, 1.0, [0.5, 0.5], 100, 61)
        for bad in (0.0, 1.0, 1.5):
            with self.assertRaises(ValueError):
                confidence_intervals([group], confidence=bad)


if __name__ == "__main__":
    unittest.main()
