import random
import unittest

from permtest.exact import exact_test
from permtest.montecarlo import monte_carlo_test, wilson_interval
from permtest.spaces import IndependentTwoSampleSpace, PairedSwapSpace


class TestWilsonInterval(unittest.TestCase):
    def test_bounds_within_unit_interval(self):
        low, high = wilson_interval(0, 100)
        self.assertGreaterEqual(low, 0.0)
        self.assertLessEqual(high, 1.0)
        self.assertLess(low, high)

    def test_shrinks_with_n(self):
        _, wide = wilson_interval(5, 100)
        _, narrow = wilson_interval(500, 10000)
        self.assertLess(narrow, wide)


class TestMonteCarlo(unittest.TestCase):
    def setUp(self):
        self.values = [9, 8, 7, 6, 5, 1, 2, 3, 4, 0]
        self.space = IndependentTwoSampleSpace(self.values, 5)

    def test_requires_random_source(self):
        with self.assertRaises(ValueError):
            monte_carlo_test(self.space, draws=100)

    def test_seed_reproducible(self):
        r1 = monte_carlo_test(self.space, draws=2000, seed=42)
        r2 = monte_carlo_test(self.space, draws=2000, seed=42)
        self.assertEqual(r1.p_estimate, r2.p_estimate)
        self.assertEqual(r1.n_extreme, r2.n_extreme)

    def test_seed_and_draws_recorded(self):
        result = monte_carlo_test(self.space, draws=500, seed=123)
        self.assertEqual(result.seed, 123)
        self.assertEqual(result.draws, 500)
        self.assertEqual(result.method, "monte-carlo (approximate)")

    def test_caller_supplied_rng(self):
        rng = random.Random(7)
        result = monte_carlo_test(self.space, draws=500, rng=rng, seed=7)
        again = monte_carlo_test(self.space, draws=500, seed=7)
        self.assertEqual(result.p_estimate, again.p_estimate)

    def test_interval_covers_exact_p(self):
        exact = exact_test(self.space)
        result = monte_carlo_test(self.space, draws=20_000, seed=2024)
        self.assertLessEqual(result.ci_low, exact.p_value)
        self.assertGreaterEqual(result.ci_high, exact.p_value)
        self.assertLess(result.ci_low, result.ci_high)

    def test_estimate_close_to_exact(self):
        exact = exact_test(self.space)
        result = monte_carlo_test(self.space, draws=20_000, seed=99)
        self.assertAlmostEqual(result.p_estimate, exact.p_value, delta=0.02)

    def test_paired_monte_carlo(self):
        space = PairedSwapSpace([1.5, -0.5, 2.0, 3.0, 1.0, 2.5])
        exact = exact_test(space)
        result = monte_carlo_test(space, draws=20_000, seed=5)
        self.assertLessEqual(result.ci_low, exact.p_value)
        self.assertGreaterEqual(result.ci_high, exact.p_value)

    def test_estimate_positive(self):
        # Even with zero extreme draws the +1 correction keeps p > 0.
        result = monte_carlo_test(self.space, draws=10, seed=1)
        self.assertGreater(result.p_estimate, 0.0)


if __name__ == "__main__":
    unittest.main()
