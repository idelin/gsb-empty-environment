import math
import unittest

from bounded_estimation import (
    ConclusionKind,
    binomial_pmf,
    exact_coverage,
    hoeffding_bernoulli_radius,
    verify_on_grid,
)


class TestBinomialPmf(unittest.TestCase):
    def test_pmf_sums_to_one(self):
        for n, p in [(1, 0.5), (10, 0.3), (40, 0.9), (25, 0.01)]:
            with self.subTest(n=n, p=p):
                self.assertAlmostEqual(math.fsum(binomial_pmf(n, p)), 1.0)

    def test_boundary_degenerate(self):
        self.assertEqual(binomial_pmf(5, 0.0), [1.0, 0.0, 0.0, 0.0, 0.0, 0.0])
        self.assertEqual(binomial_pmf(5, 1.0), [0.0, 0.0, 0.0, 0.0, 0.0, 1.0])

    def test_matches_closed_form(self):
        n, p = 12, 0.4
        pmf = binomial_pmf(n, p)
        for k in (0, 3, 12):
            exact = math.comb(n, k) * p**k * (1 - p) ** (n - k)
            self.assertAlmostEqual(pmf[k], exact)


class TestExactCoverage(unittest.TestCase):
    def test_boundary_coverage_is_one(self):
        # Strict boundary derivation: at p in {0, 1} the sample mean is
        # constant, so any non-negative radius covers with probability 1.
        radius_fn = hoeffding_bernoulli_radius(0.05)
        self.assertEqual(exact_coverage(30, 0.0, radius_fn), 1.0)
        self.assertEqual(exact_coverage(30, 1.0, radius_fn), 1.0)

    def test_hoeffding_covers_on_grid(self):
        # EXACT verification (recursion, not simulation): Hoeffding's radius
        # must cover with probability >= 1 - delta at every grid point.
        delta = 0.05
        radius_fn = hoeffding_bernoulli_radius(delta)
        for n in (5, 20, 50):
            with self.subTest(n=n):
                result = verify_on_grid(n, radius_fn, target=1.0 - delta, grid_size=101)
                self.assertEqual(result.kind, ConclusionKind.EXACT)
                self.assertTrue(
                    result.passed,
                    f"n={n}: worst coverage {result.worst_coverage} at p={result.worst_p}",
                )
                self.assertGreaterEqual(result.worst_coverage, 1.0 - delta)

    def test_verifier_rejects_too_small_radius(self):
        # A radius far below any valid bound must be caught by the recursion.
        tiny = lambda n: 0.2 / n
        result = verify_on_grid(30, tiny, target=0.95, grid_size=101)
        self.assertFalse(result.passed)
        self.assertLess(result.worst_coverage, 0.95)

    def test_zero_radius_coverage_matches_point_mass(self):
        # With radius 0, coverage is exactly P(K = round(n*p)).
        n, p = 10, 0.5
        cov = exact_coverage(n, p, lambda _: 0.0)
        self.assertAlmostEqual(cov, math.comb(10, 5) * 0.5**10)


class TestLabels(unittest.TestCase):
    def test_grid_verification_labeled_exact(self):
        result = verify_on_grid(10, hoeffding_bernoulli_radius(0.1), target=0.9)
        self.assertEqual(result.kind, ConclusionKind.EXACT)


if __name__ == "__main__":
    unittest.main()
