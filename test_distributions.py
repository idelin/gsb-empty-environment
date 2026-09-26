"""Tests for reparameterised samplers and their public formulas."""

import math
import unittest

from keyrng import bernoulli, key, normal, reparameterized_normal, uniform


class TestNormalFormula(unittest.TestCase):
    def test_matches_box_muller_computed_independently(self):
        # Independently recompute Box-Muller from the uniform stream and
        # require bit-exact agreement with the library's normal().
        k = key(777)
        n = 101  # odd on purpose: exercises pair handling
        n_uniforms = n + (n & 1)
        us = uniform(k, (n_uniforms,)).data
        expected = []
        for i in range(0, n_uniforms, 2):
            u1 = max(us[i], 2.0**-53)
            u2 = us[i + 1]
            r = math.sqrt(-2.0 * math.log(u1))
            expected.append(r * math.cos(2.0 * math.pi * u2))
            if len(expected) < n:
                expected.append(r * math.sin(2.0 * math.pi * u2))
        self.assertEqual(normal(k, (n,)).data, expected)

    def test_mean_std_reparameterisation(self):
        k = key(31)
        z = normal(k, (8,))
        shifted = normal(k, (8,), mean=2.5, std=0.5)
        for zi, si in zip(z.data, shifted.data):
            self.assertAlmostEqual(si, 2.5 + 0.5 * zi, places=15)

    def test_statistical_sanity(self):
        samples = normal(key(161), (40000,))
        mean = samples.mean()
        var = sum((v - mean) ** 2 for v in samples.data) / len(samples.data)
        self.assertAlmostEqual(mean, 0.0, delta=0.03)
        self.assertAlmostEqual(var, 1.0, delta=0.05)

    def test_invalid_parameters(self):
        k = key(1)
        with self.assertRaises(ValueError):
            normal(k, (4,), std=0.0)
        with self.assertRaises(ValueError):
            normal(k, (4,), std=-2.0)
        with self.assertRaises(ValueError):
            normal(k, (4,), mean=float("nan"))
        with self.assertRaises(ValueError):
            normal(k, (4,), std=float("inf"))


class TestBernoulli(unittest.TestCase):
    def test_matches_threshold_formula(self):
        k = key(55)
        p = 0.3
        us = uniform(k, (1000,)).data
        expected = [1.0 if u < p else 0.0 for u in us]
        self.assertEqual(bernoulli(k, p, (1000,)).data, expected)

    def test_frequency(self):
        draws = bernoulli(key(9), 0.25, (40000,))
        self.assertAlmostEqual(draws.mean(), 0.25, delta=0.02)

    def test_extreme_probabilities(self):
        k = key(3)
        self.assertEqual(bernoulli(k, 0.0, (10,)).data, [0.0] * 10)
        self.assertEqual(bernoulli(k, 1.0, (10,)).data, [1.0] * 10)

    def test_invalid_probability(self):
        k = key(1)
        for bad in (-0.01, 1.01, float("nan")):
            with self.assertRaises(ValueError):
                bernoulli(k, bad, (4,))


class TestReparameterizedNormal(unittest.TestCase):
    def test_value_and_exact_gradients(self):
        k = key(2025)
        sample = reparameterized_normal(k, (6,), mean=1.5, std=2.0)
        for v, z in zip(sample.value.data, sample.z.data):
            self.assertAlmostEqual(v, 1.5 + 2.0 * z, places=15)
        dy = uniform(key(4), (6,))
        grad_mean, grad_std = sample.backward(dy)
        self.assertAlmostEqual(grad_mean, sum(dy.data), places=12)
        self.assertAlmostEqual(
            grad_std, sum(d * z for d, z in zip(dy.data, sample.z.data)), places=12
        )

    def test_gradients_match_finite_differences(self):
        k = key(88)
        dy = uniform(key(5), (16,))

        def loss(mean, std):
            s = reparameterized_normal(k, (16,), mean=mean, std=std)
            return sum(d * v for d, v in zip(dy.data, s.value.data))

        mean0, std0 = 0.7, 1.3
        eps = 1e-6
        fd_mean = (loss(mean0 + eps, std0) - loss(mean0 - eps, std0)) / (2 * eps)
        fd_std = (loss(mean0, std0 + eps) - loss(mean0, std0 - eps)) / (2 * eps)
        grad_mean, grad_std = reparameterized_normal(k, (16,), mean0, std0).backward(dy)
        self.assertAlmostEqual(grad_mean, fd_mean, places=4)
        self.assertAlmostEqual(grad_std, fd_std, places=4)


if __name__ == "__main__":
    unittest.main()
