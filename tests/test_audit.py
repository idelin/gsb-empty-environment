"""Unit tests for the empirical audit core (synthetic data only)."""

import math
import unittest

from dp_audit import audit_mechanism, deterministic_identity, randomized_response
from dp_audit.audit import wilson_interval

EVENT = lambda out: out == 1  # noqa: E731
EVENT_DESC = "S = {output == 1}"


def rr_half(record, eps, rng):
    """0.5-DP randomized response, audited against looser claims."""
    return randomized_response(record, 0.5, rng)


class TestWilsonInterval(unittest.TestCase):
    def test_interval_contains_point_estimate(self):
        lo, hi = wilson_interval(40, 100, gamma=0.05)
        self.assertLess(lo, 0.4)
        self.assertGreater(hi, 0.4)

    def test_interval_shrinks_with_more_trials(self):
        _, hi_small = wilson_interval(40, 100, gamma=0.05)
        lo_small, _ = wilson_interval(40, 100, gamma=0.05)
        lo_big, hi_big = wilson_interval(400, 1000, gamma=0.05)
        self.assertLess(hi_big - lo_big, hi_small - lo_small)

    def test_zero_successes_not_treated_as_zero_probability(self):
        lo, hi = wilson_interval(0, 1000, gamma=0.05)
        self.assertEqual(lo, 0.0)
        # One-sided Clopper-Pearson upper bound: 1 - (gamma/2)^(1/n).
        self.assertAlmostEqual(hi, 1.0 - (0.025) ** (1.0 / 1000))
        self.assertGreater(hi, 0.0)

    def test_all_successes_symmetric(self):
        lo, hi = wilson_interval(1000, 1000, gamma=0.05)
        self.assertEqual(hi, 1.0)
        self.assertAlmostEqual(lo, (0.025) ** (1.0 / 1000))

    def test_invalid_arguments_raise(self):
        with self.assertRaises(ValueError):
            wilson_interval(-1, 100, gamma=0.05)
        with self.assertRaises(ValueError):
            wilson_interval(10, 0, gamma=0.05)
        with self.assertRaises(ValueError):
            wilson_interval(10, 100, gamma=1.5)


class TestAuditMechanism(unittest.TestCase):
    def test_dp_mechanism_consistent_not_proof(self):
        result = audit_mechanism(
            rr_half,
            eps_claimed=1.0,
            event=EVENT,
            event_description=EVENT_DESC,
            trials_per_dataset=50_000,
            alpha=0.05,
            seed=12345,
        )
        self.assertEqual(result.verdict, "consistent_not_proof")
        self.assertLessEqual(result.ratio_ci[1], math.exp(1.0))
        self.assertTrue(any("NOT prove" in n for n in result.notes))

    def test_broken_mechanism_violation_detected_and_reproducible(self):
        result = audit_mechanism(
            deterministic_identity,
            eps_claimed=1.0,
            event=EVENT,
            event_description=EVENT_DESC,
            trials_per_dataset=10_000,
            alpha=0.05,
            seed=999,
        )
        self.assertEqual(result.verdict, "violation_detected")
        self.assertGreater(result.ratio_ci[0], math.exp(1.0))
        # Zero observations on D0 must keep a positive upper bound.
        self.assertEqual(result.successes_d0, 0)
        self.assertGreater(result.ci_d0[1], 0.0)
        self.assertTrue(any("reproducible violation" in n for n in result.notes))

    def test_same_seed_reproduces_identical_result(self):
        kwargs = dict(
            eps_claimed=1.0,
            event=EVENT,
            event_description=EVENT_DESC,
            trials_per_dataset=5_000,
            alpha=0.05,
            seed=7,
        )
        first = audit_mechanism(randomized_response, **kwargs)
        second = audit_mechanism(randomized_response, **kwargs)
        self.assertEqual(first, second)

    def test_result_records_seed_and_trials(self):
        result = audit_mechanism(
            randomized_response,
            eps_claimed=0.5,
            event=EVENT,
            event_description=EVENT_DESC,
            trials_per_dataset=2_000,
            alpha=0.1,
            seed=42,
        )
        self.assertEqual(result.seed, 42)
        self.assertEqual(result.trials_per_dataset, 2_000)
        self.assertEqual(result.alpha, 0.1)
        self.assertEqual(
            result.successes_d0 + (result.trials_per_dataset - result.successes_d0),
            2_000,
        )

    def test_invalid_arguments_raise(self):
        with self.assertRaises(ValueError):
            audit_mechanism(randomized_response, 1.0, EVENT, EVENT_DESC,
                            trials_per_dataset=0)
        with self.assertRaises(ValueError):
            audit_mechanism(randomized_response, -1.0, EVENT, EVENT_DESC)
        with self.assertRaises(ValueError):
            audit_mechanism(randomized_response, 1.0, EVENT, EVENT_DESC,
                            alpha=0.0)


if __name__ == "__main__":
    unittest.main()
