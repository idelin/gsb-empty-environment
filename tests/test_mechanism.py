import math
import unittest

from dp_synth import Rng, diagnose_laplace, laplace_mechanism
from dp_synth.synthetic_data import make_synthetic_employee_data


class MechanismTests(unittest.TestCase):
    def test_seeded_reproducibility(self):
        a = [laplace_mechanism(0.0, 1.0, 1.0, Rng(123)) for _ in range(5)]
        b = [laplace_mechanism(0.0, 1.0, 1.0, Rng(123)) for _ in range(5)]
        self.assertEqual(a, b)

    def test_scale_follows_sensitivity_over_epsilon(self):
        diag1 = diagnose_laplace(1000.0, epsilon=1.0, sensitivity=1.0, trials=20000, seed=1)
        diag2 = diagnose_laplace(1000.0, epsilon=0.5, sensitivity=1.0, trials=20000, seed=1)
        self.assertAlmostEqual(diag1.expected_scale, 1.0, places=10)
        self.assertAlmostEqual(diag2.expected_scale, 2.0, places=10)
        self.assertLess(abs(diag1.observed_bias), 0.05)
        self.assertLess(abs(diag2.observed_bias), 0.1)
        # Laplace stddev = scale * sqrt(2)
        self.assertAlmostEqual(diag1.observed_stddev, math.sqrt(2.0), delta=0.1)
        self.assertAlmostEqual(diag2.observed_stddev, 2.0 * math.sqrt(2.0), delta=0.2)
        # P(|noise| <= b) = 1 - exp(-1) ~ 0.632
        self.assertAlmostEqual(diag1.empirical_coverage_epsilon, 1 - math.exp(-1), delta=0.02)

    def test_synthetic_generator_is_deterministic(self):
        d1 = make_synthetic_employee_data(n=50, seed=7)
        d2 = make_synthetic_employee_data(n=50, seed=7)
        self.assertEqual(d1.records, d2.records)
        self.assertEqual(d1.version, d2.version)


if __name__ == "__main__":
    unittest.main()
