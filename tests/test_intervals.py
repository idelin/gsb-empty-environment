"""Tests for the outward-rounded Decimal interval arithmetic."""

import math
import unittest
from decimal import Context, Decimal

from subsample_amp import Interval

TRUTH = Context(prec=90)  # higher-precision context used as "exact" oracle


class TestContainment(unittest.TestCase):
    def test_add_mul_outward(self):
        a = Interval(Decimal(1) / 3)
        b = Interval(Decimal(2) / 7)
        s = a + b
        self.assertTrue(s.contains(TRUTH.add(a.lo, b.lo)))
        p = a * b
        self.assertTrue(p.contains(TRUTH.multiply(a.lo, b.lo)))
        self.assertLessEqual(s.lo, TRUTH.add(a.lo, b.lo))
        self.assertGreaterEqual(s.hi, TRUTH.add(a.lo, b.lo))

    def test_division(self):
        q = Interval(1) / Interval(3)
        self.assertTrue(q.contains(TRUTH.divide(Decimal(1), Decimal(3))))
        with self.assertRaises(ZeroDivisionError):
            Interval(1) / Interval(-1, 2)

    def test_exp_ln_expm1_log1p(self):
        x = Interval(Decimal("0.7"))
        self.assertTrue(x.exp().contains(TRUTH.exp(x.lo)))
        self.assertTrue(x.ln().contains(TRUTH.ln(x.lo)))
        self.assertTrue(x.expm1().contains(TRUTH.subtract(TRUTH.exp(x.lo), Decimal(1))))
        self.assertTrue(x.log1p().contains(TRUTH.ln(TRUTH.add(Decimal(1), x.lo))))

    def test_interval_width_is_tiny(self):
        x = Interval(Decimal("0.7"))
        width = x.exp().hi - x.exp().lo
        self.assertLess(abs(width), Decimal(10) ** -55)

    def test_float_conversion_rounds_outward(self):
        iv = Interval(Decimal(1) / 3)
        lo_f, hi_f = iv.to_float()
        self.assertLessEqual(lo_f, float(iv.lo))
        self.assertGreaterEqual(hi_f, float(iv.hi))
        # At most a couple of ulps wide.
        self.assertLessEqual(hi_f - lo_f, 4 * math.ulp(float(iv.lo)))

    def test_float_conversion_of_exact_float(self):
        iv = Interval(0.1)  # exact binary value of the float 0.1
        lo_f, hi_f = iv.to_float()
        self.assertLessEqual(lo_f, 0.1)
        self.assertGreaterEqual(hi_f, 0.1)

    def test_ln_of_zero_lower_bound_is_minus_inf(self):
        iv = Interval(0, 1).ln()
        self.assertEqual(iv.lo, Decimal("-Infinity"))
        self.assertTrue(iv.contains(0))  # ln(1) = 0

    def test_rejects_non_finite(self):
        with self.assertRaises(ValueError):
            Interval(float("nan"))
        with self.assertRaises(ValueError):
            Interval(float("inf"))


if __name__ == "__main__":
    unittest.main()
