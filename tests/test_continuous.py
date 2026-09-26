"""Tests for the numeric (floating-point, approximate) Laplace interface."""

import math
import unittest
from random import Random

from dpcount.continuous import (
    LaplaceRecord,
    ParameterError,
    add_laplace_noise,
    laplace_inverse_cdf,
)


class FixedRNG:
    def __init__(self, u):
        self.u = u

    def random(self):
        return self.u


class TestLaplace(unittest.TestCase):
    def test_nonpositive_epsilon_rejected(self):
        for bad in (0.0, -0.5):
            with self.assertRaises(ParameterError):
                add_laplace_noise(10, 1, bad, Random(0))

    def test_negative_sensitivity_rejected(self):
        with self.assertRaises(ParameterError):
            add_laplace_noise(10, -1, 1.0, Random(0))

    def test_inverse_cdf_known_values_and_symmetry(self):
        self.assertAlmostEqual(laplace_inverse_cdf(0.5, 1.0), 0.0)
        self.assertAlmostEqual(laplace_inverse_cdf(0.75, 1.0), math.log(2))
        self.assertAlmostEqual(
            laplace_inverse_cdf(0.25, 1.0), -math.log(2)
        )
        self.assertAlmostEqual(
            laplace_inverse_cdf(0.1, 2.0), -laplace_inverse_cdf(0.9, 2.0)
        )

    def test_endpoint_uniform_inputs_are_errors_not_resamples(self):
        for u in (0.0, 1.0):
            with self.assertRaises(ParameterError):
                laplace_inverse_cdf(u, 1.0)
            with self.assertRaises(ParameterError):
                add_laplace_noise(10, 1, 1.0, FixedRNG(u))

    def test_deterministic_input_recomputes_same_output(self):
        u = 0.314159265
        rec = add_laplace_noise(10, 1, 1.0, FixedRNG(u))
        self.assertIsInstance(rec, LaplaceRecord)
        self.assertEqual(rec.uniform_input, u)
        self.assertAlmostEqual(rec.raw_noise, laplace_inverse_cdf(u, 1.0))
        self.assertAlmostEqual(rec.raw_noisy_count, 10.0 + rec.raw_noise)
        seeded_a = add_laplace_noise(10, 1, 1.0, Random(7)).raw_noisy_count
        seeded_b = add_laplace_noise(10, 1, 1.0, Random(7)).raw_noisy_count
        self.assertEqual(seeded_a, seeded_b)

    def test_negative_noise_kept_raw_postprocessed_separately(self):
        # u < 0.5 gives negative noise; u=0.01, scale 1 -> ln(0.02) ~ -3.9
        rec = add_laplace_noise(2, 1, 1.0, FixedRNG(0.01))
        self.assertLess(rec.raw_noisy_count, 0)
        self.assertEqual(rec.postprocessed_count, 0.0)
        self.assertIn("no resampling", rec.postprocessing)
        self.assertTrue(rec.mechanism.startswith("continuous-laplace"))

    def test_zero_sensitivity_adds_no_noise(self):
        rec = add_laplace_noise(5, 0, 1.0, FixedRNG(0.3))
        self.assertEqual(rec.raw_noise, 0.0)
        self.assertEqual(rec.raw_noisy_count, 5.0)

    def test_statistical_smoke_test_only_not_a_proof(self):
        rng = Random(99)
        n = 40000
        scale = 2.0
        samples = [laplace_inverse_cdf(rng.random(), scale) for _ in range(n)]
        mean = sum(samples) / n
        variance = sum((z - mean) ** 2 for z in samples) / n
        self.assertAlmostEqual(mean, 0.0, delta=0.05)
        self.assertAlmostEqual(variance, 2 * scale**2, delta=0.5)


if __name__ == "__main__":
    unittest.main()
