"""Tests for the exact-rational discrete Laplace count mechanism."""

import unittest
from fractions import Fraction
from random import Random

from dpcount.discrete import (
    DiscreteLaplaceCount,
    ParameterError,
    rational_decay_for_epsilon,
)


class ScriptedBits:
    """Deterministic stand-in for random.Random exposing getrandbits."""

    def __init__(self, bits):
        self._bits = list(bits)
        self.emitted = []

    @property
    def consumed(self):
        return len(self.emitted)

    def getrandbits(self, k):
        result = 0
        for _ in range(k):
            bit = self._bits[len(self.emitted) % len(self._bits)]
            self.emitted.append(bit)
            result = (result << 1) | bit
        return result


class TestParameters(unittest.TestCase):
    def test_decay_must_be_rational_in_open_unit_interval(self):
        for bad in (0, 1, -1, 2, Fraction(0), Fraction(1)):
            with self.assertRaises(ParameterError):
                DiscreteLaplaceCount(1, bad, -2, 2)

    def test_sensitivity_must_be_positive_integer(self):
        for bad in (0, -1):
            with self.assertRaises(ParameterError):
                DiscreteLaplaceCount(bad, Fraction(1, 2), -2, 2)

    def test_nonempty_domain(self):
        with self.assertRaises(ParameterError):
            DiscreteLaplaceCount(1, Fraction(1, 2), 2, 1)

    def test_rational_decay_for_epsilon_rejects_nonpositive_epsilon(self):
        for bad in (0.0, -1.0):
            with self.assertRaises(ParameterError):
                rational_decay_for_epsilon(bad, 1)

    def test_rational_decay_rounds_up_against_exponential(self):
        import math

        p = rational_decay_for_epsilon(1.0, 1, precision_bits=32)
        self.assertEqual(p.denominator, 1 << 32)
        self.assertGreaterEqual(float(p), math.exp(-1.0))
        self.assertLess(p, 1)


class TestExactDistribution(unittest.TestCase):
    def setUp(self):
        self.mech = DiscreteLaplaceCount(1, Fraction(1, 2), -2, 2)

    def test_probabilities_are_exact_and_sum_to_one(self):
        # count 0: weights 1/4, 1/2, 1, 1/2, 1/4 -> Z = 5/2
        expected = {
            -2: Fraction(1, 10),
            -1: Fraction(1, 5),
            0: Fraction(2, 5),
            1: Fraction(1, 5),
            2: Fraction(1, 10),
        }
        total = Fraction(0)
        for x, want in expected.items():
            got = self.mech.probability(x, 0)
            self.assertEqual(got, want)
            self.assertIsInstance(got, Fraction)
            total += got
        self.assertEqual(total, Fraction(1))

    def test_probability_zero_outside_domain_full_support_inside(self):
        self.assertEqual(self.mech.probability(-3, 0), Fraction(0))
        self.assertEqual(self.mech.probability(3, 0), Fraction(0))
        for x in range(-2, 3):
            self.assertGreater(self.mech.probability(x, 7), Fraction(0))

    def test_inverse_cdf_exact_boundaries(self):
        # cells for count 0: [0,1/10), [1/10,3/10), [3/10,7/10),
        #                    [7/10,9/10), [9/10,1)
        cases = {
            Fraction(0): -2,
            Fraction(1, 10) - Fraction(1, 10**9): -2,
            Fraction(1, 10): -1,   # boundary belongs to the upper cell
            Fraction(3, 10): 0,
            Fraction(7, 10): 1,
            Fraction(9, 10): 2,
        }
        for u, want in cases.items():
            self.assertEqual(self.mech.inverse_cdf(0, u), want, u)

    def test_inverse_cdf_rejects_outside_unit_interval(self):
        for bad in (Fraction(-1, 2), Fraction(1), Fraction(3, 2)):
            with self.assertRaises(ParameterError):
                self.mech.inverse_cdf(0, bad)


class TestSampler(unittest.TestCase):
    def test_seeded_rng_is_reproducible(self):
        m = DiscreteLaplaceCount(1, Fraction(1, 3), -6, 6)
        a = [m.sample(0, Random(12345)) for _ in range(50)]
        b = [m.sample(0, Random(12345)) for _ in range(50)]
        self.assertEqual(a, b)
        self.assertTrue(all(-6 <= x <= 6 for x in a))

    def test_sampler_matches_deterministic_inverse_cdf(self):
        # With a known bit prefix consumed up to the decision, the sampler's
        # output must equal inverse_cdf at the interval's lower endpoint.
        m = DiscreteLaplaceCount(1, Fraction(2, 5), -3, 3)
        for prefix in ([0, 0], [1, 1], [0, 1, 0, 1], [1, 0, 1, 0]):
            bits = ScriptedBits(prefix)
            x = m.sample(0, bits)
            k = bits.consumed
            u = Fraction(int("".join(map(str, bits.emitted)), 2), 1 << k)
            self.assertEqual(x, m.inverse_cdf(0, u))

    def test_negative_raw_noise_is_kept_not_resampled(self):
        # All-zero bits drive the dyadic interval toward 0 -> smallest output.
        m = DiscreteLaplaceCount(1, Fraction(1, 2), -5, 5)
        rec = m.noisy_count(0, ScriptedBits([0] * 64))
        self.assertLess(rec.raw, 0)
        self.assertEqual(rec.postprocessed, 0)
        self.assertIn("no resampling", rec.postprocessing)

    def test_frequency_check_is_only_a_smoke_test_not_a_proof(self):
        # Statistical sanity check with a fixed seed; deliberately loose.
        m = DiscreteLaplaceCount(1, Fraction(1, 2), -3, 3)
        rng = Random(20260926)
        n = 20000
        counts = {x: 0 for x in range(-3, 4)}
        for _ in range(n):
            counts[m.sample(0, rng)] += 1
        for x in range(-3, 4):
            freq = counts[x] / n
            self.assertAlmostEqual(freq, float(m.probability(x, 0)), delta=0.02)


if __name__ == "__main__":
    unittest.main()
