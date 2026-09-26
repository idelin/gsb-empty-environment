import math
import unittest
from fractions import Fraction

from bayes import BayesianUpdater, ConflictError, exact_evidence, exact_posterior


class TestExactReference(unittest.TestCase):
    def test_exact_rational_posterior(self):
        priors = {"a": Fraction(1, 2), "b": Fraction(1, 2)}
        obs = [{"a": Fraction(3, 4), "b": Fraction(1, 4)}]
        post = exact_posterior(priors, obs)
        self.assertEqual(post["a"], Fraction(3, 4))
        self.assertEqual(post["b"], Fraction(1, 4))

    def test_exact_evidence_value(self):
        priors = {"a": Fraction(1, 2), "b": Fraction(1, 2)}
        obs = [{"a": Fraction(3, 4), "b": Fraction(1, 4)}]
        self.assertEqual(exact_evidence(priors, obs), Fraction(1, 2))

    def test_exact_zero_evidence_conflicts(self):
        priors = {"a": Fraction(1, 2), "b": Fraction(1, 2)}
        obs = [{"a": 0, "b": 1}, {"a": 1, "b": 0}]
        self.assertEqual(exact_evidence(priors, obs), 0)
        with self.assertRaises(ConflictError):
            exact_posterior(priors, obs)

    def test_cross_check_float_updater_against_fractions(self):
        priors = {"a": 0.5, "b": 0.3, "c": 0.2}
        obs = [
            {"a": 0.25, "b": 0.5, "c": 0.75},
            {"a": 0.1, "b": 0.9, "c": 0.4},
            {"a": 0.8, "b": 0.2, "c": 0.6},
        ]
        u = BayesianUpdater(priors)
        u.apply_batch(enumerate(obs))
        exact = exact_posterior(priors, obs)
        for h in priors:
            self.assertAlmostEqual(u.posterior()[h], float(exact[h]), places=12)

    def test_log_evidence_matches_exact(self):
        priors = {"a": 0.5, "b": 0.3, "c": 0.2}
        obs = [{"a": 0.25, "b": 0.5, "c": 0.75}, {"a": 0.1, "b": 0.9, "c": 0.4}]
        u = BayesianUpdater(priors)
        u.apply_batch(enumerate(obs))
        self.assertAlmostEqual(
            math.exp(u.log_evidence), float(exact_evidence(priors, obs)), places=12
        )


if __name__ == "__main__":
    unittest.main()
