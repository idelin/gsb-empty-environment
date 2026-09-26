"""Deterministic, exact tests for the DP counting mechanisms.

All privacy checks here are *formal* (exhaustive enumeration with exact
rational arithmetic).  No statistical/sampling-frequency argument is used as
evidence of privacy anywhere in this test suite.
"""

import math
import random
import unittest
from fractions import Fraction

from dpcount import (
    DiscreteLaplaceMechanism,
    LaplaceMechanism,
    exp_neg_upper_bound,
)


class TestParameterValidation(unittest.TestCase):
    def test_laplace_rejects_nonpositive_epsilon(self):
        for bad in (0.0, -1.0, -0.5):
            with self.assertRaises(ValueError):
                LaplaceMechanism(sensitivity=1, epsilon=bad)

    def test_discrete_rejects_nonpositive_epsilon(self):
        for bad in (0, -1, Fraction(-1, 10), 0.0):
            with self.assertRaises(ValueError):
                DiscreteLaplaceMechanism(max_count=10, sensitivity=1, epsilon=bad)

    def test_discrete_rejects_bad_domain(self):
        with self.assertRaises(ValueError):
            DiscreteLaplaceMechanism(max_count=0, sensitivity=1, epsilon=1.0)
        with self.assertRaises(ValueError):
            DiscreteLaplaceMechanism(max_count=10, sensitivity=0, epsilon=1.0)


class TestExactExpBound(unittest.TestCase):
    def test_zero(self):
        self.assertEqual(exp_neg_upper_bound(Fraction(0)), Fraction(1))

    def test_is_provable_upper_bound(self):
        # r = 1/S_n with S_n a Taylor partial sum of exp(x) (all terms
        # positive), hence r >= exp(-x) exactly.  Check against float exp
        # with a conservative margin, plus tightness of the bound.
        for x in (Fraction(1, 10), Fraction(1), Fraction(2), Fraction(37, 10)):
            r = exp_neg_upper_bound(x)
            self.assertIsInstance(r, Fraction)
            self.assertGreaterEqual(float(r), math.exp(-float(x)))
            self.assertLess(float(r) - math.exp(-float(x)), 1e-12)

    def test_rejects_negative_x(self):
        with self.assertRaises(ValueError):
            exp_neg_upper_bound(Fraction(-1))


class TestContinuousLaplace(unittest.TestCase):
    def test_inverse_cdf_is_deterministic_and_matches_formula(self):
        mech = LaplaceMechanism(sensitivity=2, epsilon=0.5)
        b = 2 / 0.5
        for u in (0.01, 0.25, 0.5, 0.75, 0.99):
            expected = -b * math.copysign(1.0, u - 0.5) * math.log(1 - 2 * abs(u - 0.5))
            self.assertEqual(mech.inverse_cdf(u), expected)
            # reproducible: same input, same output
            self.assertEqual(mech.inverse_cdf(u), mech.inverse_cdf(u))
        self.assertEqual(mech.inverse_cdf(0.5), 0.0)

    def test_inverse_cdf_rejects_out_of_range(self):
        mech = LaplaceMechanism(sensitivity=1, epsilon=1.0)
        for bad in (0.0, 1.0, -0.1, 1.1):
            with self.assertRaises(ValueError):
                mech.inverse_cdf(bad)

    def test_negative_noise_is_kept_not_resampled(self):
        calls = []

        def uniform():
            calls.append(1)
            return 0.01  # maps to a large negative noise draw

        mech = LaplaceMechanism(sensitivity=1, epsilon=1.0, uniform=uniform)
        result = mech.noisy_count(true_count=3)
        self.assertEqual(len(calls), 1, "exactly one uniform draw; no resampling")
        self.assertLess(result.raw, 0.0, "raw noisy count went negative and is kept")
        self.assertEqual(result.postprocessed, 0.0)
        self.assertNotEqual(result.raw, result.postprocessed,
                            "raw and post-processed values are recorded separately")

    def test_raw_and_postprocessed_recorded_separately(self):
        mech = LaplaceMechanism(sensitivity=1, epsilon=1.0, uniform=lambda: 0.9)
        result = mech.noisy_count(true_count=7)
        self.assertGreater(result.raw, 7.0)
        self.assertEqual(result.postprocessed, result.raw)  # no clamping needed
        self.assertFalse(result.exact)  # continuous Laplace: numerical approximation
        self.assertTrue(any("approximation" in n for n in result.notes))

    def test_reproducible_with_seeded_source(self):
        seq1 = LaplaceMechanism(sensitivity=1, epsilon=1.0,
                                uniform=random.Random(123).random)
        seq2 = LaplaceMechanism(sensitivity=1, epsilon=1.0,
                                uniform=random.Random(123).random)
        for _ in range(20):
            self.assertEqual(seq1.sample_noise(), seq2.sample_noise())


class TestDiscreteDistribution(unittest.TestCase):
    def setUp(self):
        self.mech = DiscreteLaplaceMechanism(max_count=8, sensitivity=1,
                                             epsilon=Fraction(1, 2))

    def test_probabilities_exact_positive_and_sum_to_one(self):
        for c in range(self.mech.max_count + 1):
            probs = self.mech.probabilities(c)
            self.assertEqual(sum(probs), Fraction(1))
            for p in probs:
                self.assertIsInstance(p, Fraction)
                self.assertGreater(p, 0, "no zero-probability outputs on the domain")

    def test_weights_match_r_powers(self):
        r = self.mech.r
        c = 3
        probs = self.mech.probabilities(c)
        # Ratio of neighbouring probabilities must be exactly r (interior).
        self.assertEqual(probs[c + 1] / probs[c], r)
        self.assertEqual(probs[c - 1] / probs[c], r)

    def test_inverse_cdf_partitions_unit_interval_exactly(self):
        # Small rational base so the denominator Z is tiny: exhaustively map
        # every k in [0, Z) and confirm each output y gets exactly w_y of the
        # k's -- i.e. the integer sampling is exactly unbiased.
        mech = DiscreteLaplaceMechanism.from_base_r(max_count=4, sensitivity=1,
                                                    r=Fraction(1, 2))
        c = 2
        weights = mech.weights(c)
        total = sum(weights)
        counts = [0] * (mech.max_count + 1)
        for k in range(total):
            counts[mech.inverse_cdf(c, k)] += 1
        self.assertEqual(counts, weights)

    def test_inverse_cdf_deterministic(self):
        mech = DiscreteLaplaceMechanism.from_base_r(max_count=4, sensitivity=1,
                                                    r=Fraction(1, 2))
        total = sum(mech.weights(2))
        for k in (0, 1, total // 2, total - 1):
            self.assertEqual(mech.inverse_cdf(2, k), mech.inverse_cdf(2, k))
        with self.assertRaises(ValueError):
            mech.inverse_cdf(2, total)
        with self.assertRaises(ValueError):
            mech.inverse_cdf(2, -1)

    def test_sampling_uses_injected_source_and_is_reproducible(self):
        m1 = DiscreteLaplaceMechanism(max_count=8, sensitivity=1, epsilon=1.0,
                                      randbelow=random.Random(7).randrange)
        m2 = DiscreteLaplaceMechanism(max_count=8, sensitivity=1, epsilon=1.0,
                                      randbelow=random.Random(7).randrange)
        draws1 = [m1.sample(4) for _ in range(50)]
        draws2 = [m2.sample(4) for _ in range(50)]
        self.assertEqual(draws1, draws2)
        self.assertTrue(all(0 <= y <= 8 for y in draws1))

    def test_sample_matches_inverse_cdf_of_drawn_k(self):
        drawn = []

        def randbelow(n):
            k = random.Random(len(drawn)).randrange(n)  # deterministic sequence
            drawn.append((n, k))
            return k

        mech = DiscreteLaplaceMechanism(max_count=6, sensitivity=1, epsilon=1.0,
                                        randbelow=randbelow)
        y = mech.sample(3)
        n, k = drawn[0]
        self.assertEqual(y, mech.inverse_cdf(3, k))
        self.assertEqual(n, sum(mech.weights(3)))


class TestFormalVerification(unittest.TestCase):
    """Exhaustive exact checks -- these are the formal privacy arguments."""

    def test_exhaustive_dp_check_passes(self):
        mech = DiscreteLaplaceMechanism(max_count=8, sensitivity=1,
                                        epsilon=Fraction(1, 2))
        result = mech.verify_dp()
        self.assertTrue(result.ok)
        self.assertIsNone(result.counterexample)
        # Every adjacent ordered pair and every output was enumerated.
        self.assertEqual(result.pairs_checked, 2 * 8)
        self.assertEqual(result.outputs_checked, 2 * 8 * 9)

    def test_max_ratio_is_exactly_r_to_minus_stride(self):
        mech = DiscreteLaplaceMechanism(max_count=8, sensitivity=1,
                                        epsilon=Fraction(1, 2))
        result = mech.verify_dp()
        # Interior adjacent pairs attain the bound exactly.
        self.assertEqual(result.max_ratio, 1 / mech.r)
        self.assertEqual(result.bound, 1 / mech.r)

    def test_multi_record_subject_sensitivity(self):
        # Subject may contribute up to 2 records -> sensitivity 2.
        mech = DiscreteLaplaceMechanism(max_count=10, sensitivity=2,
                                        epsilon=Fraction(1))
        result = mech.verify_dp()
        self.assertTrue(result.ok)
        self.assertEqual(result.max_ratio, mech.r ** -2)
        # The guarantee r**-sensitivity <= exp(epsilon) holds by construction
        # (r >= exp(-epsilon/sensitivity)); sanity-check numerically.
        self.assertLessEqual(float(mech.privacy_loss_bound()), math.exp(1.0) + 1e-12)

    def test_verifier_has_teeth(self):
        # Claiming a tighter bound than the mechanism provides must fail,
        # with an exact rational counterexample.
        mech = DiscreteLaplaceMechanism(max_count=8, sensitivity=1,
                                        epsilon=Fraction(1, 2))
        result = mech.verify_dp(claimed_r=mech.r * 2)
        self.assertFalse(result.ok)
        self.assertIsNotNone(result.counterexample)
        c, c2, y, ratio = result.counterexample
        self.assertGreater(ratio, (mech.r * 2) ** -abs(c2 - c))

    def test_larger_stride_still_satisfies_composed_bound(self):
        mech = DiscreteLaplaceMechanism(max_count=8, sensitivity=1,
                                        epsilon=Fraction(1, 2))
        result = mech.verify_dp(stride=3)
        self.assertTrue(result.ok)
        self.assertLessEqual(result.max_ratio, mech.r ** -3)


if __name__ == "__main__":
    unittest.main()
