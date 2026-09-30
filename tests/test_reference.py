"""Cross-checks against the independent high-precision series reference."""

import unittest
from decimal import Context, Decimal

from subsample_amp import (
    Const,
    SubsampleAmp,
    reference_amp,
    series_exp,
    series_ln,
    subsampled_epsilon,
)

ORACLE = Context(prec=100)


class TestSeriesAgainstDecimalBuiltins(unittest.TestCase):
    def test_series_exp(self):
        # Hand-rolled series agree with Decimal's correctly rounded exp
        # to ~80 digits; the series then independently validate the
        # interval evaluator (which uses the builtins).
        for x in ("0.1", "1", "3.7", "10", "100", "700"):
            got = series_exp(Decimal(x), prec=80)
            want = ORACLE.exp(Decimal(x))
            self.assertLess(abs(got - want) / want, Decimal(10) ** -70, x)

    def test_series_ln(self):
        for y in ("0.01", "0.5", "1.5", "2", "12345.678"):
            got = series_ln(Decimal(y), prec=80)
            want = ORACLE.ln(Decimal(y))
            self.assertLess(abs(got - want), Decimal(10) ** -70, y)


class TestReferenceContainedInIntervals(unittest.TestCase):
    GRID = (
        (1e-3, 1e-6),
        (0.1, 0.5),
        (1.0, 0.01),
        (1.0, 0.999),
        (10.0, 0.5),
        (50.0, 0.9),
        (700.0, 0.25),
        (1000.0, 0.01),
        (1000.0, 1e-300),
    )

    def test_reference_inside_dag_interval(self):
        for eps, q in self.GRID:
            ref = reference_amp(eps, q)
            iv = SubsampleAmp(Const(eps), Const(q)).evaluate()
            self.assertTrue(iv.contains(ref), (eps, q, ref, iv))

    def test_float_value_inside_float_hull(self):
        for eps, q in self.GRID:
            iv = SubsampleAmp(Const(eps), Const(q)).evaluate()
            # Margin absorbs the float evaluation's own rounding (a few ulps).
            lo_f, hi_f = iv.to_float(margin_ulp=8)
            val = subsampled_epsilon(eps, q)
            self.assertLessEqual(lo_f, val, (eps, q))
            self.assertLessEqual(val, hi_f, (eps, q))

    def test_float_value_matches_reference(self):
        for eps, q in self.GRID:
            ref = reference_amp(eps, q)
            val = Decimal(subsampled_epsilon(eps, q))
            scale = max(abs(ref), Decimal(1))
            self.assertLess(abs(val - ref) / scale, Decimal(10) ** -12, (eps, q))


class TestReferenceBoundaries(unittest.TestCase):
    def test_exact_boundaries(self):
        self.assertEqual(reference_amp(5.0, 0.0), 0)
        self.assertEqual(reference_amp(0.0, 0.7), 0)
        self.assertEqual(reference_amp(5.0, 1.0), Decimal(5.0))


if __name__ == "__main__":
    unittest.main()
