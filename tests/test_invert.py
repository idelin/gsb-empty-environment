"""Tests for inverting the amplification formula (max q for a budget)."""

import math
import unittest
from decimal import Decimal

from subsample_amp import (
    ApplicabilityError,
    max_q_for_budget,
    subsampled_epsilon,
)


class TestInversionRoundTrip(unittest.TestCase):
    def test_simple_round_trip(self):
        eps, target = 1.0, 0.3
        res = max_q_for_budget(eps, target)
        expected = math.expm1(target) / math.expm1(eps)
        self.assertLessEqual(res.q_lo, expected)
        self.assertGreaterEqual(res.q_hi, expected)
        self.assertTrue(res.bracket_verified)
        tol = 1e-12 * max(1.0, target)
        self.assertLessEqual(res.residual_lo, tol)
        self.assertGreaterEqual(res.residual_hi, -tol)
        self.assertAlmostEqual(
            subsampled_epsilon(eps, (res.q_lo + res.q_hi) / 2), target, places=12
        )

    def test_grid_verified(self):
        for eps in (0.01, 0.5, 1.0, 5.0, 50.0, 500.0):
            for frac in (0.1, 0.5, 0.9):
                target = frac * eps
                res = max_q_for_budget(eps, target)
                self.assertTrue(res.bracket_verified, (eps, target))
                self.assertTrue(res.interval_contains_target, (eps, target))
                tol = 1e-12 * max(1.0, target)
                self.assertLessEqual(res.amp_at_q_lo, target + tol)
                self.assertLessEqual(target - tol, res.amp_at_q_hi)

    def test_huge_eps_no_overflow(self):
        # exp(700) is representable but exp(1000) is not; both must work.
        res = max_q_for_budget(700.0, 10.0)
        self.assertTrue(res.bracket_verified)
        self.assertGreater(res.q_lo, 0.0)
        res = max_q_for_budget(1000.0, 5.0)
        self.assertTrue(res.bracket_verified)
        self.assertGreater(res.q_interval.hi, 0)

    def test_subnormal_bracket_documented(self):
        res = max_q_for_budget(1000.0, 5.0)
        # q* ~ 1.5e-436 is far below the smallest float subnormal.
        self.assertEqual(res.q_lo, 0.0)
        self.assertTrue(any("subnormal" in note for note in res.notes))
        self.assertTrue(res.bracket_verified)

    def test_astronomical_eps_no_overflow(self):
        # q* ~ exp(-1e300) is unrepresentable even as a Decimal; the
        # bracket must degenerate gracefully without any exp overflow.
        res = max_q_for_budget(1e300, 5.0)
        self.assertEqual(res.q_lo, 0.0)
        self.assertGreater(res.q_hi, 0.0)
        self.assertTrue(res.bracket_verified)


class TestInversionBoundaries(unittest.TestCase):
    def test_target_zero_gives_q_zero(self):
        res = max_q_for_budget(2.0, 0.0)
        self.assertEqual((res.q_lo, res.q_hi), (0.0, 0.0))
        self.assertTrue(res.bracket_verified)

    def test_target_at_eps_gives_q_one(self):
        res = max_q_for_budget(2.0, 2.0)
        self.assertEqual((res.q_lo, res.q_hi), (1.0, 1.0))
        self.assertTrue(res.bracket_verified)

    def test_target_above_eps_gives_q_one(self):
        res = max_q_for_budget(2.0, 5.0)
        self.assertEqual((res.q_lo, res.q_hi), (1.0, 1.0))
        self.assertEqual(res.amp_at_q_hi, 2.0)

    def test_eps_zero_allows_any_q(self):
        res = max_q_for_budget(0.0, 0.5)
        self.assertEqual((res.q_lo, res.q_hi), (1.0, 1.0))
        self.assertTrue(res.bracket_verified)


class TestInversionRejection(unittest.TestCase):
    def test_invalid_inputs_rejected(self):
        with self.assertRaises(ApplicabilityError):
            max_q_for_budget(-1.0, 0.5)
        with self.assertRaises(ApplicabilityError):
            max_q_for_budget(1.0, -0.5)
        with self.assertRaises(ApplicabilityError):
            max_q_for_budget(float("nan"), 0.5)
        with self.assertRaises(ApplicabilityError):
            max_q_for_budget(1.0, 0.5, model="uniform_without_replacement")
        with self.assertRaises(ApplicabilityError):
            max_q_for_budget(1.0, 0.5, base_delta=1e-5)

    def test_decimal_bracket_is_rigorous(self):
        res = max_q_for_budget(3.0, 1.0)
        # The Decimal bracket must contain the exact quotient.
        from decimal import Context

        oracle = Context(prec=90)
        exact = oracle.divide(
            oracle.subtract(oracle.exp(Decimal(1)), Decimal(1)),
            oracle.subtract(oracle.exp(Decimal(3)), Decimal(1)),
        )
        self.assertLessEqual(res.q_interval.lo, exact)
        self.assertGreaterEqual(res.q_interval.hi, exact)


if __name__ == "__main__":
    unittest.main()
