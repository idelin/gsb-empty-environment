import math
import random
import unittest

from dp_audit.stats import (
    binomial_cdf,
    binomial_sf,
    clopper_pearson,
    one_sided_zero_upper,
)


class TestBinomialTails(unittest.TestCase):
    def test_cdf_edges(self):
        self.assertAlmostEqual(binomial_cdf(0, 10, 0.5), 0.5**10, places=12)
        self.assertEqual(binomial_cdf(10, 10, 0.3), 1.0)
        self.assertEqual(binomial_cdf(-1, 10, 0.3), 0.0)

    def test_sf_is_complement_of_cdf(self):
        self.assertAlmostEqual(
            binomial_sf(4, 10, 0.3), 1.0 - binomial_cdf(3, 10, 0.3), places=9
        )

    def test_degenerate_p(self):
        self.assertEqual(binomial_cdf(0, 5, 0.0), 1.0)
        self.assertEqual(binomial_sf(1, 5, 0.0), 0.0)
        self.assertEqual(binomial_sf(5, 5, 1.0), 1.0)


class TestClopperPearson(unittest.TestCase):
    def test_zero_count_is_not_zero_probability(self):
        lower, upper = clopper_pearson(0, 100, 0.05)
        self.assertEqual(lower, 0.0)
        self.assertGreater(upper, 0.0)
        self.assertAlmostEqual(upper, 1.0 - 0.025 ** (1.0 / 100), places=9)

    def test_full_count(self):
        lower, upper = clopper_pearson(100, 100, 0.05)
        self.assertEqual(upper, 1.0)
        self.assertAlmostEqual(lower, 0.025 ** (1.0 / 100), places=9)

    def test_interval_brackets_mle(self):
        k, n = 37, 100
        lower, upper = clopper_pearson(k, n, 0.05)
        self.assertLess(lower, k / n)
        self.assertGreater(upper, k / n)

    def test_interval_widens_with_higher_confidence(self):
        lo_95, hi_95 = clopper_pearson(30, 100, 0.05)
        lo_99, hi_99 = clopper_pearson(30, 100, 0.01)
        self.assertLess(lo_99, lo_95)
        self.assertGreater(hi_99, hi_95)

    def test_interval_shifts_with_k(self):
        lo_a, hi_a = clopper_pearson(20, 100, 0.05)
        lo_b, hi_b = clopper_pearson(40, 100, 0.05)
        self.assertLess(lo_a, lo_b)
        self.assertLess(hi_a, hi_b)

    def test_empirical_coverage_at_least_nominal(self):
        # Clopper-Pearson is conservative: coverage must be >= 1 - alpha.
        rng = random.Random(7)
        reps, n, p, alpha = 400, 120, 0.3, 0.05
        covered = 0
        for _ in range(reps):
            k = sum(1 for _ in range(n) if rng.random() < p)
            lo, hi = clopper_pearson(k, n, alpha)
            if lo <= p <= hi:
                covered += 1
        self.assertGreaterEqual(covered / reps, 0.95)

    def test_invalid_inputs(self):
        with self.assertRaises(ValueError):
            clopper_pearson(5, 0, 0.05)
        with self.assertRaises(ValueError):
            clopper_pearson(6, 5, 0.05)
        with self.assertRaises(ValueError):
            clopper_pearson(5, 5, 0.0)


class TestRuleOfThree(unittest.TestCase):
    def test_exact_form(self):
        n = 1000
        self.assertAlmostEqual(
            one_sided_zero_upper(n, 0.05), 1.0 - 0.05 ** (1.0 / n), places=15
        )

    def test_approximates_three_over_n(self):
        n = 1000
        self.assertAlmostEqual(one_sided_zero_upper(n, 0.05), 3.0 / n, delta=1e-3)

    def test_shrinks_with_n(self):
        self.assertLess(
            one_sided_zero_upper(10000, 0.05), one_sided_zero_upper(100, 0.05)
        )


if __name__ == "__main__":
    unittest.main()
