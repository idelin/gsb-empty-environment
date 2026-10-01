import random
import unittest

from factorelim import (
    ContradictoryEvidenceError,
    FactorGraph,
    estimate_query,
    query,
)


def make_sprinkler():
    """Rain -> Sprinkler, Rain -> WetGrass, Sprinkler -> WetGrass."""
    g = FactorGraph()
    for v in ("R", "S", "W"):
        g.add_variable(v, (0, 1))
    g.add_factor(("R",), [0.8, 0.2])
    g.add_factor(("R", "S"), [0.6, 0.4, 0.99, 0.01])
    g.add_factor(("R", "S", "W"),
                 [0.95, 0.05, 0.10, 0.90, 0.20, 0.80, 0.01, 0.99])
    return g


class TestSampling(unittest.TestCase):
    def test_seed_and_sample_count_recorded(self):
        g = make_sprinkler()
        res = estimate_query(g, ("R",), n_samples=500,
                             rng=random.Random(42), seed=42)
        self.assertEqual(res.seed, 42)
        self.assertEqual(res.n_samples, 500)
        self.assertEqual(res.method, "empirical")
        self.assertIn("[EMPIRICAL]", str(res))
        self.assertIn("seed=42", str(res))

    def test_rng_must_be_injected(self):
        g = make_sprinkler()
        with self.assertRaises(ValueError):
            estimate_query(g, ("R",), n_samples=10, rng=None)

    def test_reproducible_with_same_seed(self):
        g = make_sprinkler()
        a = estimate_query(g, ("R",), n_samples=200,
                           rng=random.Random(7), seed=7)
        b = estimate_query(g, ("R",), n_samples=200,
                           rng=random.Random(7), seed=7)
        self.assertEqual(a.probabilities, b.probabilities)
        self.assertEqual(a.normalizer, b.normalizer)

    def test_empirical_close_to_exact(self):
        g = make_sprinkler()
        exact = query(g, ("R",), evidence={"W": 1})
        est = estimate_query(g, ("R",), evidence={"W": 1},
                             n_samples=60000, rng=random.Random(1), seed=1)
        for key, p in exact.probabilities.items():
            self.assertAlmostEqual(est.probabilities[key], p, delta=0.02)
        self.assertAlmostEqual(est.normalizer, exact.normalizer, delta=0.02)

    def test_contradictory_evidence_raises(self):
        g = FactorGraph()
        g.add_variable("A", (0, 1))
        g.add_factor(("A",), [1.0, 0.0])
        with self.assertRaises(ContradictoryEvidenceError):
            estimate_query(g, ("A",), evidence={"A": 1},
                           n_samples=100, rng=random.Random(0), seed=0)

    def test_zero_probability_cells_supported(self):
        g = FactorGraph()
        g.add_variable("A", (0, 1))
        g.add_factor(("A",), [1.0, 0.0])  # A=1 never sampled with weight
        est = estimate_query(g, ("A",), n_samples=200,
                             rng=random.Random(3), seed=3)
        self.assertEqual(est.probabilities[(1,)], 0.0)
        self.assertEqual(est.probabilities[(0,)], 1.0)
        self.assertLess(est.n_nonzero, est.n_samples)


if __name__ == "__main__":
    unittest.main()
