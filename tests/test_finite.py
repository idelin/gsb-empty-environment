import math
import unittest

from bounded_mean import (
    bernoulli_coverage_exact,
    binomial_pmf,
    verify_hoeffding_bernoulli,
    verify_radius_bernoulli,
)


def pmf_bruteforce(n, p):
    return [
        math.comb(n, k) * p**k * (1.0 - p) ** (n - k) for k in range(n + 1)
    ]


class TestBinomialRecursion(unittest.TestCase):
    def test_matches_bruteforce(self):
        for n, p in ((1, 0.3), (10, 0.5), (25, 0.13)):
            for a, b in zip(binomial_pmf(n, p), pmf_bruteforce(n, p)):
                self.assertAlmostEqual(a, b, places=12)

    def test_normalised(self):
        self.assertAlmostEqual(sum(binomial_pmf(40, 0.7)), 1.0, places=12)

    def test_boundary_degenerate(self):
        self.assertEqual(binomial_pmf(5, 0.0), [1.0, 0, 0, 0, 0, 0])
        self.assertEqual(binomial_pmf(5, 1.0), [0, 0, 0, 0, 0, 1.0])


class TestExactCoverage(unittest.TestCase):
    def test_boundary_coverage_is_one(self):
        # Strict boundary derivation: p in {0,1} makes the sample mean
        # constant, so any non-negative radius covers with probability 1.
        self.assertEqual(bernoulli_coverage_exact(50, 0.0, 0.01), 1.0)
        self.assertEqual(bernoulli_coverage_exact(50, 1.0, 0.01), 1.0)

    def test_known_value(self):
        # n=4, p=0.5, radius=0.25 covers k in {1,2,3}: 14/16.
        self.assertAlmostEqual(bernoulli_coverage_exact(4, 0.5, 0.25), 14 / 16)

    def test_hoeffding_verified_exactly_on_grid(self):
        report = verify_hoeffding_bernoulli(30, 0.05, grid_points=101)
        self.assertEqual(report.conclusion_kind, "exact")
        self.assertTrue(report.ok)
        self.assertGreaterEqual(report.min_coverage, 0.95)

    def test_too_small_radius_fails_verification(self):
        # A radius far below any valid bound must be rejected exactly.
        report = verify_radius_bernoulli(30, 0.01, 0.05, grid_points=101)
        self.assertFalse(report.ok)
        self.assertLess(report.min_coverage, 0.95)


if __name__ == "__main__":
    unittest.main()
