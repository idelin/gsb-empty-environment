import random
import unittest
from fractions import Fraction

from bayes import empirical_identification_rate, exact_identification_rate

PRIORS = {"fair": 0.5, "loaded": 0.5}
CHANNELS = {
    "fair": {"H": 0.5, "T": 0.5},
    "loaded": {"H": 0.7, "T": 0.3},
}


class TestSimulation(unittest.TestCase):
    def test_exact_label_and_value(self):
        est = exact_identification_rate(
            priors=PRIORS, channels=CHANNELS, true_hyp="loaded", n_obs=4
        )
        self.assertEqual(est.label, "exact")
        self.assertIsInstance(est.value, Fraction)
        self.assertIsNone(est.seed)
        self.assertIsNone(est.samples)
        self.assertGreater(est.value, Fraction(1, 2))

    def test_empirical_records_seed_and_samples(self):
        rng = random.Random(42)
        est = empirical_identification_rate(
            rng,
            priors=PRIORS,
            channels=CHANNELS,
            true_hyp="loaded",
            n_obs=10,
            n_trials=500,
            seed=42,
        )
        self.assertEqual(est.label, "empirical")
        self.assertEqual(est.seed, 42)
        self.assertEqual(est.samples, 500)
        self.assertGreaterEqual(est.value, 0.0)
        self.assertLessEqual(est.value, 1.0)

    def test_empirical_reproducible_with_same_seed(self):
        kwargs = dict(
            priors=PRIORS, channels=CHANNELS, true_hyp="loaded",
            n_obs=10, n_trials=300, seed=7,
        )
        a = empirical_identification_rate(random.Random(7), **kwargs)
        b = empirical_identification_rate(random.Random(7), **kwargs)
        self.assertEqual(a.value, b.value)

    def test_empirical_tracks_exact(self):
        exact = exact_identification_rate(
            priors=PRIORS, channels=CHANNELS, true_hyp="loaded", n_obs=8
        )
        emp = empirical_identification_rate(
            random.Random(123),
            priors=PRIORS, channels=CHANNELS, true_hyp="loaded",
            n_obs=8, n_trials=4000, seed=123,
        )
        self.assertAlmostEqual(emp.value, float(exact.value), delta=0.03)


if __name__ == "__main__":
    unittest.main()
