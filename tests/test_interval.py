import unittest

from binpower.exact import binomial_pmf_table
from binpower.interval import clopper_pearson, regularized_beta


class TestRegularizedBeta(unittest.TestCase):
    def test_endpoints(self):
        self.assertEqual(regularized_beta(0.0, 2.0, 3.0), 0.0)
        self.assertEqual(regularized_beta(1.0, 2.0, 3.0), 1.0)

    def test_known_value(self):
        # I_0.5(2, 2) = 0.5 by symmetry.
        self.assertAlmostEqual(regularized_beta(0.5, 2.0, 2.0), 0.5, places=12)
        # I_x(1, 1) = x.
        self.assertAlmostEqual(regularized_beta(0.3, 1.0, 1.0), 0.3, places=12)

    def test_invalid_params(self):
        with self.assertRaises(ValueError):
            regularized_beta(0.5, 0.0, 1.0)


class TestClopperPearson(unittest.TestCase):
    def test_zero_trials_refused(self):
        with self.assertRaises(ValueError):
            clopper_pearson(0, 0)

    def test_invalid_counts(self):
        with self.assertRaises(ValueError):
            clopper_pearson(5, 4)
        with self.assertRaises(ValueError):
            clopper_pearson(-1, 4)

    def test_zero_successes_closed_form(self):
        m, alpha = 10, 0.05
        lo, hi = clopper_pearson(0, m, alpha)
        self.assertEqual(lo, 0.0)
        self.assertAlmostEqual(hi, 1.0 - (alpha / 2.0) ** (1.0 / m), places=12)

    def test_all_rejections_still_have_width(self):
        m, alpha = 10, 0.05
        lo, hi = clopper_pearson(m, m, alpha)
        self.assertEqual(hi, 1.0)
        self.assertAlmostEqual(lo, (alpha / 2.0) ** (1.0 / m), places=12)
        self.assertGreater(hi - lo, 0.0)  # interval never collapses to a point

    def test_exact_coverage(self):
        # Exact (not simulated) coverage check: sum the binomial pmf over all
        # k whose interval contains the true p; must be >= 1 - alpha.
        m, p, alpha = 25, 0.3, 0.1
        pmf = binomial_pmf_table(m, p)
        coverage = 0.0
        for k in range(m + 1):
            lo, hi = clopper_pearson(k, m, alpha)
            if lo <= p <= hi:
                coverage += pmf[k]
        self.assertGreaterEqual(coverage, 1.0 - alpha)

    def test_monotone_in_k(self):
        bounds = [clopper_pearson(k, 30, 0.05) for k in range(31)]
        lows = [b[0] for b in bounds]
        highs = [b[1] for b in bounds]
        self.assertTrue(all(a <= b for a, b in zip(lows, lows[1:])))
        self.assertTrue(all(a <= b for a, b in zip(highs, highs[1:])))


if __name__ == "__main__":
    unittest.main()
