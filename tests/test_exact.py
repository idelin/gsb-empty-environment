import math
import unittest
from math import comb

from binomial_power import binom_cdf, binom_pmf_all, binom_sf, critical_value, exact_power


class TestBinomPmf(unittest.TestCase):
    def test_matches_combinatorial_formula(self):
        n, p = 12, 0.37
        pmf = binom_pmf_all(n, p)
        for k in range(n + 1):
            expect = comb(n, k) * p**k * (1 - p) ** (n - k)
            self.assertAlmostEqual(pmf[k], expect, places=12)

    def test_normalised(self):
        for n, p in [(1, 0.5), (50, 0.1), (200, 0.9), (1000, 0.001)]:
            self.assertAlmostEqual(math.fsum(binom_pmf_all(n, p)), 1.0, places=12)

    def test_degenerate_p(self):
        self.assertEqual(binom_pmf_all(5, 0.0), [1.0] + [0.0] * 5)
        self.assertEqual(binom_pmf_all(5, 1.0), [0.0] * 5 + [1.0])

    def test_sf_cdf_consistent(self):
        n, p = 30, 0.4
        for k in range(n + 1):
            self.assertAlmostEqual(binom_sf(k, n, p) + binom_cdf(k - 1, n, p), 1.0, places=12)

    def test_sf_bounds(self):
        self.assertEqual(binom_sf(0, 10, 0.5), 1.0)
        self.assertEqual(binom_sf(11, 10, 0.5), 0.0)
        self.assertEqual(binom_cdf(-1, 10, 0.5), 0.0)
        self.assertEqual(binom_cdf(10, 10, 0.5), 1.0)


class TestCriticalValueAndPower(unittest.TestCase):
    def test_size_never_exceeds_alpha(self):
        for n in (10, 37, 100):
            for alpha in (0.01, 0.05, 0.1):
                _, size = critical_value(n, 0.5, alpha)
                self.assertLessEqual(size, alpha)

    def test_critical_is_minimal(self):
        n, p0, alpha = 40, 0.3, 0.05
        c, size = critical_value(n, p0, alpha)
        self.assertLessEqual(size, alpha)
        self.assertGreater(binom_sf(c - 1, n, p0), alpha)

    def test_exact_power_monotone_in_p1(self):
        n, p0, alpha = 50, 0.5, 0.05
        c, _, _ = exact_power(n, p0, p1=0.6, alpha=alpha)
        powers = [binom_sf(c, n, p) for p in (0.5, 0.6, 0.7, 0.8)]
        self.assertEqual(powers, sorted(powers))

    def test_exact_power_known_value(self):
        # hand-checkable: n=1, p0=0.5, alpha=0.5 -> c=1, power at p1 is p1
        c, size, power = exact_power(1, 0.5, 0.8, alpha=0.5)
        self.assertEqual(c, 1)
        self.assertAlmostEqual(size, 0.5)
        self.assertAlmostEqual(power, 0.8)


if __name__ == "__main__":
    unittest.main()
