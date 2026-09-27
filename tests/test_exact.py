import math
import unittest

from binpower.exact import (
    binomial_cdf_table,
    binomial_pmf_table,
    binomial_tail,
    critical_value,
    exact_power,
)


class TestPmfTable(unittest.TestCase):
    def test_known_values(self):
        table = binomial_pmf_table(2, 0.5)
        self.assertEqual(len(table), 3)
        for got, want in zip(table, [0.25, 0.5, 0.25]):
            self.assertAlmostEqual(got, want, places=15)

    def test_sums_to_one(self):
        for n, p in [(1, 0.3), (10, 0.5), (57, 0.13), (200, 0.9)]:
            with self.subTest(n=n, p=p):
                self.assertAlmostEqual(sum(binomial_pmf_table(n, p)), 1.0, places=12)

    def test_large_n_no_underflow(self):
        # Mode-anchored recursion must not underflow where (1-p)**n would.
        table = binomial_pmf_table(5000, 0.5)
        self.assertAlmostEqual(sum(table), 1.0, places=10)
        self.assertGreater(max(table), 0.0)

    def test_degenerate_p(self):
        self.assertEqual(binomial_pmf_table(5, 0.0), [1.0, 0.0, 0.0, 0.0, 0.0, 0.0])
        self.assertEqual(binomial_pmf_table(5, 1.0), [0.0, 0.0, 0.0, 0.0, 0.0, 1.0])

    def test_invalid_inputs(self):
        with self.assertRaises(ValueError):
            binomial_pmf_table(-1, 0.5)
        with self.assertRaises(ValueError):
            binomial_pmf_table(3, 1.5)


class TestTailAndCdf(unittest.TestCase):
    def test_tail_matches_pmf_sum(self):
        n, p = 40, 0.35
        pmf = binomial_pmf_table(n, p)
        for c in (0, 1, 14, 40, 41):
            with self.subTest(c=c):
                want = sum(pmf[c:]) if c <= n else 0.0
                self.assertAlmostEqual(binomial_tail(n, p, c), want, places=12)

    def test_cdf_last_entry_exactly_one(self):
        cdf = binomial_cdf_table(77, 0.42)
        self.assertEqual(cdf[-1], 1.0)
        self.assertTrue(all(a <= b for a, b in zip(cdf, cdf[1:])))

    def test_degenerate_tail(self):
        self.assertEqual(binomial_tail(10, 0.0, 1), 0.0)
        self.assertEqual(binomial_tail(10, 1.0, 10), 1.0)
        self.assertEqual(binomial_tail(10, 0.5, 0), 1.0)
        self.assertEqual(binomial_tail(10, 0.5, 11), 0.0)


class TestCriticalValue(unittest.TestCase):
    def test_size_respects_alpha_and_is_minimal(self):
        n, p0, alpha = 20, 0.5, 0.05
        c = critical_value(n, p0, alpha)
        size = binomial_tail(n, p0, c)
        self.assertLessEqual(size, alpha)
        # Minimality: lowering the threshold by one must exceed alpha.
        self.assertGreater(binomial_tail(n, p0, c - 1), alpha)

    def test_known_value(self):
        # Bin(20, 0.5): P(X >= 15) = 0.0207 <= 0.05, P(X >= 14) = 0.0577 > 0.05.
        self.assertEqual(critical_value(20, 0.5, 0.05), 15)

    def test_impossible_rejection_when_alpha_tiny(self):
        n, p0 = 5, 0.5
        c = critical_value(n, p0, 0.01)  # p0**5 = 0.03125 > 0.01
        self.assertEqual(c, n + 1)
        self.assertEqual(binomial_tail(n, p0, c), 0.0)

    def test_invalid_inputs(self):
        with self.assertRaises(ValueError):
            critical_value(0, 0.5, 0.05)
        with self.assertRaises(ValueError):
            critical_value(10, 0.5, 1.5)


class TestExactPower(unittest.TestCase):
    def test_labeled_exact(self):
        res = exact_power(50, 0.5, 0.65, 0.05)
        self.assertEqual(res.kind, "exact")
        self.assertGreater(res.power, res.size)
        self.assertLessEqual(res.size, 0.05)

    def test_power_monotone_in_alternative(self):
        n, p0, alpha = 60, 0.5, 0.05
        c = critical_value(n, p0, alpha)
        powers = [binomial_tail(n, p, c) for p in (0.5, 0.6, 0.7, 0.8)]
        self.assertTrue(all(a < b for a, b in zip(powers, powers[1:])))

    def test_matches_reference_numbers(self):
        # Hand-checkable: n=20, p0=0.5, alpha=0.05 -> c=15.
        res = exact_power(20, 0.5, 0.7, 0.05)
        self.assertEqual(res.critical, 15)
        self.assertAlmostEqual(res.size, 0.020694732666015625, places=15)
        self.assertAlmostEqual(res.power, 0.4163708294474812, places=12)


if __name__ == "__main__":
    unittest.main()
