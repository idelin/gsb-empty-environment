"""Formal exhaustive DP ratio verification (not sampling-frequency based)."""

import unittest
from fractions import Fraction

from dpcount.discrete import DiscreteLaplaceCount, verify_dp


def brute_force_max_ratio(mech, c_lo, c_hi):
    """Independent, deliberately slow reference implementation in the test."""
    best = Fraction(0)
    where = None
    for c in range(c_lo, c_hi + 1):
        for cp in range(c_lo, c_hi + 1):
            d = abs(c - cp)
            if d == 0 or d > mech.sensitivity:
                continue
            for x in range(mech.lo, mech.hi + 1):
                r = mech.probability(x, c) / mech.probability(x, cp)
                if r > best:
                    best, where = r, (c, cp, x)
    return best, where


class TestVerification(unittest.TestCase):
    def test_small_domain_exact_ratio_and_pair_count(self):
        mech = DiscreteLaplaceCount(1, Fraction(1, 2), -1, 1)
        report = verify_dp(mech, 0, 2)
        # counts 0,1,2 -> ordered adjacent pairs:
        # (0,1),(1,0),(1,2),(2,1) = 4 pairs, 3 outputs each = 12 ratios
        self.assertEqual(report.adjacent_pairs, 4)
        self.assertEqual(report.ratios_checked, 12)
        best, where = brute_force_max_ratio(mech, 0, 2)
        self.assertEqual(report.max_ratio, best)
        self.assertEqual(report.argmax, where)
        self.assertGreaterEqual(report.max_ratio, Fraction(1))

    def test_truncation_naive_bound_can_fail_boundary_case(self):
        # Domain {0,1,2}, p=1/2: ratio at (c=0, c'=1, x=0) is 16/7 > 2 = 1/p.
        mech = DiscreteLaplaceCount(1, Fraction(1, 2), 0, 2)
        naive = Fraction(2)
        exact = mech.probability(0, 0) / mech.probability(0, 1)
        self.assertEqual(exact, Fraction(16, 7))
        self.assertGreater(exact, naive)
        report = verify_dp(mech, 0, 2, claimed_max_ratio=naive)
        self.assertFalse(report.claimed_bound_holds)
        self.assertEqual(report.max_ratio, Fraction(16, 7))

    def test_exact_tight_bound_is_certified(self):
        mech = DiscreteLaplaceCount(1, Fraction(3, 7), -4, 4)
        report = verify_dp(mech, 0, 6)
        tight = report.max_ratio
        self.assertTrue(
            verify_dp(mech, 0, 6, claimed_max_ratio=tight).claimed_bound_holds
        )
        self.assertFalse(
            verify_dp(
                mech, 0, 6, claimed_max_ratio=tight - Fraction(1, 10**30)
            ).claimed_bound_holds
        )

    def test_sensitivity_two_skips_distance_one_only_pairs(self):
        mech = DiscreteLaplaceCount(2, Fraction(1, 3), -3, 3)
        report = verify_dp(mech, 0, 4)
        # counts 0..4: ordered pairs at distance 1 or 2
        expected_pairs = sum(
            1 for i in range(5) for j in range(5)
            if 1 <= abs(i - j) <= 2
        )
        self.assertEqual(report.adjacent_pairs, expected_pairs)
        self.assertEqual(
            report.ratios_checked, expected_pairs * (3 - (-3) + 1)
        )

    def test_empty_count_domain_rejected(self):
        mech = DiscreteLaplaceCount(1, Fraction(1, 2), 0, 2)
        with self.assertRaises(ValueError):
            verify_dp(mech, 3, 1)


if __name__ == "__main__":
    unittest.main()
