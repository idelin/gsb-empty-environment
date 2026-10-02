"""Static curve compilation: continuity, slope conditions, monotonicity,
independent analytic reference, identity, silence safety, inverse lookup."""
import math
import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from dyncurve import DB_FLOOR, TOLERANCES, Segment, compile_curve
from dyncurve.curve import CONTINUITY_TOL

TOL = TOLERANCES["static_gain_db"]


def reference_output_db(x, segments):
    """Independent analytic reference (hard-knee only), written directly
    from the spec: each region between consecutive thresholds has the slope
    of its own segment (1/R compress at left edge, R expand at right edge,
    else 1); output is anchored to zero gain at the lowest threshold."""
    segs = sorted(segments, key=lambda s: s.threshold_db)

    def slope(j):
        left = segs[j - 1] if j > 0 else None
        right = segs[j] if j < len(segs) else None
        if left is not None and left.kind == "compress":
            return 1.0 / left.ratio
        if right is not None and right.kind == "expand":
            return right.ratio
        return 1.0

    y = segs[0].threshold_db
    if x <= segs[0].threshold_db:
        return y + slope(0) * (x - segs[0].threshold_db)
    for i in range(1, len(segs)):
        if x <= segs[i].threshold_db:
            return y + slope(i) * (x - segs[i - 1].threshold_db)
        y += slope(i) * (segs[i].threshold_db - segs[i - 1].threshold_db)
    return y + slope(len(segs)) * (x - segs[-1].threshold_db)


def reference_gain_db(x, segments):
    return reference_output_db(x, segments) - x


class TestCompileChecks(unittest.TestCase):
    def test_compile_checks_pass_for_valid_curve(self):
        curve = compile_curve([
            Segment(-60.0, 2.0, kind="expand"),
            Segment(-30.0, 4.0, knee_db=10.0),
            Segment(-10.0, 20.0),
        ])
        by_name = {c.name: c for c in curve.checks}
        self.assertIn("value_continuity", by_name)
        self.assertIn("slope_conditions", by_name)
        self.assertIn("monotonicity", by_name)
        for c in curve.checks:
            self.assertTrue(c.ok, c.detail)

    def test_rejects_duplicate_thresholds(self):
        with self.assertRaises(ValueError):
            compile_curve([Segment(-20.0, 2.0), Segment(-20.0, 3.0)])

    def test_rejects_overlapping_knees(self):
        with self.assertRaises(ValueError):
            compile_curve([Segment(-20.0, 2.0, knee_db=12.0),
                           Segment(-24.0, 2.0, knee_db=12.0)])

    def test_rejects_bad_ratio(self):
        with self.assertRaises(ValueError):
            Segment(-20.0, 0.5)


class TestIdentityAndSilence(unittest.TestCase):
    def test_ratio_one_is_identity(self):
        curve = compile_curve([Segment(-40.0, 1.0), Segment(-10.0, 1.0)])
        for x in (-120.0, -60.0, -39.9, -20.0, -0.1, 0.0):
            self.assertAlmostEqual(curve.gain_db(x), 0.0, delta=TOL)
            self.assertAlmostEqual(curve.output_db(x), x, delta=TOL)

    def test_silence_never_produces_infinity(self):
        curve = compile_curve([Segment(-60.0, 3.0, kind="expand"),
                               Segment(-20.0, 8.0)])
        for x in (DB_FLOOR, DB_FLOOR - 1.0, -1e9, float("-inf")):
            g = curve.gain_db(x)
            self.assertTrue(math.isfinite(g))
            self.assertTrue(math.isfinite(curve.output_db(x)))


class TestHardKneeReference(unittest.TestCase):
    """Independent analytic reference checked at and around segment edges."""

    def test_multisegment_boundaries(self):
        segs = [Segment(-60.0, 2.0, kind="expand"),
                Segment(-30.0, 4.0),
                Segment(-10.0, 20.0)]
        curve = compile_curve(segs)
        probe = [-100.0, -60.0, -59.999, -60.001, -45.0, -30.0, -30.001,
                 -29.999, -20.0, -10.0, -10.001, -9.999, -1.0, 0.0]
        for x in probe:
            self.assertAlmostEqual(curve.gain_db(x), reference_gain_db(x, segs),
                                   delta=TOL, msg=f"x={x}")

    def test_value_continuity_at_hard_knee(self):
        segs = [Segment(-30.0, 4.0)]
        curve = compile_curve(segs)
        eps = 1e-7
        self.assertAlmostEqual(curve.output_db(-30.0 - eps),
                               curve.output_db(-30.0 + eps), delta=1e-5)


class TestSoftKnee(unittest.TestCase):
    def test_value_and_slope_continuous_at_both_knee_ends(self):
        t, w, r = -24.0, 12.0, 4.0
        curve = compile_curve([Segment(t, r, knee_db=w)])
        eps = 1e-6
        for edge in (t - w / 2.0, t + w / 2.0):
            v_in = curve.output_db(edge)
            v_out_l = curve.output_db(edge - eps)
            v_out_r = curve.output_db(edge + eps)
            self.assertAlmostEqual(v_in, v_out_l, delta=1e-5)
            self.assertAlmostEqual(v_in, v_out_r, delta=1e-5)
            # numeric slopes on both sides of the edge match
            s_l = (curve.output_db(edge - eps) - curve.output_db(edge - 2 * eps)) / eps
            s_r = (curve.output_db(edge + 2 * eps) - curve.output_db(edge + eps)) / eps
            self.assertAlmostEqual(s_l, s_r, delta=1e-3)

    def test_soft_knee_gain_at_centre(self):
        # classic soft-knee: at threshold, gain = (1/R - 1) * W/8
        t, w, r = -24.0, 12.0, 4.0
        curve = compile_curve([Segment(t, r, knee_db=w)])
        expected = (1.0 / r - 1.0) * w / 8.0
        self.assertAlmostEqual(curve.gain_db(t), expected, delta=1e-9)


class TestMonotonicity(unittest.TestCase):
    def test_output_non_decreasing(self):
        curve = compile_curve([Segment(-70.0, 2.5, kind="expand"),
                               Segment(-30.0, 3.0, knee_db=8.0),
                               Segment(-8.0, math.inf)])
        xs = [-160.0 + 0.5 * i for i in range(321)]
        ys = [curve.output_db(x) for x in xs]
        for a, b in zip(ys, ys[1:]):
            self.assertLessEqual(a, b + CONTINUITY_TOL)


class TestInverse(unittest.TestCase):
    def setUp(self):
        self.curve = compile_curve([Segment(-60.0, 2.0, kind="expand"),
                                    Segment(-30.0, 4.0, knee_db=10.0)])

    def test_round_trip(self):
        for x in (-100.0, -70.0, -60.0, -45.0, -30.0, -10.0, 0.0):
            y = self.curve.output_db(x)
            lo, hi = self.curve.inverse_db(y)
            self.assertLessEqual(lo, x + TOLERANCES["inverse_db"])
            self.assertGreaterEqual(hi, x - TOLERANCES["inverse_db"])

    def test_plateau_is_non_invertible_interval(self):
        curve = compile_curve([Segment(-12.0, math.inf)])
        plateaus = curve.plateaus()
        self.assertEqual(len(plateaus), 1)
        x0, x1, y = plateaus[0]
        self.assertEqual(x0, -12.0)
        lo, hi = curve.inverse_db(y)
        self.assertEqual((lo, hi), (x0, x1))
        self.assertFalse(curve.is_invertible_at(y))
        # inside the plateau any input maps to the same output
        self.assertAlmostEqual(curve.output_db(-6.0), y, delta=TOL)
        self.assertAlmostEqual(curve.output_db(0.0), y, delta=TOL)

    def test_out_of_range_returns_none(self):
        curve = compile_curve([Segment(-12.0, math.inf)])
        self.assertIsNone(curve.inverse_db(0.0))  # above plateau output


if __name__ == "__main__":
    unittest.main()
