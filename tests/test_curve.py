"""Static curve tests: continuity, derivatives, monotonicity, inverse,
analytic references, silence safety, compile-time validation."""
import math
import random
import unittest

from dyncurve import COMPRESS, EXPAND, DB_FLOOR, Segment, compile_curve
from dyncurve.curve import INVERSE_X_MAX


def ref_soft_compress(x, t, r, w):
    """Independent closed-form soft-knee compressor reference."""
    if w > 0.0 and abs(x - t) <= 0.5 * w:
        return x + (1.0 / r - 1.0) * (x - t + 0.5 * w) ** 2 / (2.0 * w)
    if x > t:
        return t + (x - t) / r
    return x


def ref_multi_compress(x, t1, r1, t2, r2):
    """Independent two-breakpoint hard-knee compressor reference."""
    if x < t1:
        return x
    if x < t2:
        return t1 + (x - t1) / r1
    return t1 + (t2 - t1) / r1 + (x - t2) / (r1 * r2)


def ref_expander(x, t, r):
    """Independent hard downward-expander reference."""
    return x if x > t else t + (x - t) * r


class TestIdentity(unittest.TestCase):
    def test_ratio_one_is_identity(self):
        curve = compile_curve([Segment(-20.0, 1.0, knee_db=6.0)])
        for x in (-100.0, -20.0, -17.0, 0.0, 12.0):
            self.assertEqual(curve.output_db(x), x)

    def test_empty_is_identity(self):
        curve = compile_curve([])
        for x in (-120.0, -3.5, 0.0, 24.0):
            self.assertEqual(curve.output_db(x), x)


class TestSoftKnee(unittest.TestCase):
    def setUp(self):
        self.t, self.r, self.w = -20.0, 4.0, 10.0
        self.curve = compile_curve(
            [Segment(self.t, self.r, knee_db=self.w)])

    def test_matches_independent_reference(self):
        rng = random.Random(7)
        for _ in range(500):
            x = rng.uniform(-80.0, 10.0)
            self.assertAlmostEqual(
                self.curve.output_db(x),
                ref_soft_compress(x, self.t, self.r, self.w),
                places=9)

    def test_value_continuous_at_both_knee_edges(self):
        for edge in (self.t - self.w / 2, self.t + self.w / 2):
            y_edge = self.curve.output_db(edge)
            for eps in (1e-6, 1e-9):
                # both sides converge to y(edge): no jump at the boundary
                tol = 2.0 * eps + 1e-12
                self.assertAlmostEqual(self.curve.output_db(edge - eps),
                                       y_edge, delta=tol)
                self.assertAlmostEqual(self.curve.output_db(edge + eps),
                                       y_edge, delta=tol)

    def test_slope_continuous_at_both_knee_edges(self):
        for edge in (self.t - self.w / 2, self.t + self.w / 2):
            ds = abs(self.curve.slope(edge - 1e-7)
                     - self.curve.slope(edge + 1e-7))
            self.assertLess(ds, 1e-5)
        # analytic slopes at the edges: 1 below, 1/R above
        self.assertAlmostEqual(
            self.curve.slope(self.t - self.w / 2 - 1e-9), 1.0, places=6)
        self.assertAlmostEqual(
            self.curve.slope(self.t + self.w / 2 + 1e-9), 1.0 / self.r,
            places=6)


class TestMultiSegment(unittest.TestCase):
    def test_two_breakpoint_compressor_matches_reference(self):
        t1, r1, t2, r2 = -30.0, 2.0, -10.0, 8.0
        curve = compile_curve([Segment(t1, r1), Segment(t2, r2)])
        rng = random.Random(11)
        points = [t1, t2] + [rng.uniform(-90, 20) for _ in range(500)]
        for x in points:
            self.assertAlmostEqual(curve.output_db(x),
                                   ref_multi_compress(x, t1, r1, t2, r2),
                                   places=9)

    def test_expander_matches_reference(self):
        t, r = -50.0, 2.5
        curve = compile_curve([Segment(t, r, kind=EXPAND)])
        rng = random.Random(13)
        for _ in range(500):
            x = rng.uniform(-110.0, 0.0)
            self.assertAlmostEqual(curve.output_db(x),
                                   ref_expander(x, t, r), places=9)

    def test_mixed_curve_is_monotone(self):
        curve = compile_curve([
            Segment(-60.0, 2.0, knee_db=4.0, kind=EXPAND),
            Segment(-24.0, 4.0, knee_db=8.0, kind=COMPRESS),
            Segment(-6.0, math.inf, kind=COMPRESS),
        ])
        xs = [-120.0 + i * 0.1 for i in range(1500)]
        ys = [curve.output_db(x) for x in xs]
        for a, b in zip(ys, ys[1:]):
            self.assertLessEqual(a, b + 1e-12)


class TestSilence(unittest.TestCase):
    def test_silence_is_finite(self):
        curve = compile_curve([
            Segment(-60.0, 3.0, kind=EXPAND),
            Segment(-20.0, 4.0, knee_db=8.0),
        ])
        for v in (curve.output_db(DB_FLOOR), curve.gain_db(DB_FLOOR)):
            self.assertTrue(math.isfinite(v))


class TestInverse(unittest.TestCase):
    def setUp(self):
        self.curve = compile_curve([
            Segment(-50.0, 2.0, kind=EXPAND),
            Segment(-24.0, 4.0, knee_db=8.0),
        ])

    def test_round_trip(self):
        rng = random.Random(17)
        for _ in range(300):
            x = rng.uniform(-100.0, 5.0)
            inv = self.curve.invert(self.curve.output_db(x))
            self.assertFalse(inv.is_plateau)
            self.assertLessEqual(inv.x_lo - 1e-6, x)
            self.assertLessEqual(x, inv.x_hi + 1e-6)
            self.assertAlmostEqual(inv.x_lo, inv.x_hi, places=6)

    def test_plateau_interval_not_invertible(self):
        limiter = compile_curve([Segment(-6.0, math.inf)])
        y_plateau = limiter.output_db(0.0)
        inv = limiter.invert(y_plateau)
        self.assertTrue(inv.is_plateau)
        self.assertAlmostEqual(inv.x_lo, -6.0, places=6)
        self.assertAlmostEqual(inv.x_hi, INVERSE_X_MAX, places=6)
        regions = limiter.plateau_regions()
        self.assertEqual(len(regions), 1)
        self.assertAlmostEqual(regions[0][0], -6.0, places=9)
        self.assertAlmostEqual(regions[0][2], y_plateau, places=9)

    def test_strictly_monotone_curve_has_no_plateau(self):
        self.assertEqual(self.curve.plateau_regions(), [])


class TestCompileValidation(unittest.TestCase):
    def test_bad_ratio_rejected(self):
        with self.assertRaises(ValueError):
            compile_curve([Segment(-10.0, 0.0)])
        with self.assertRaises(ValueError):
            compile_curve([Segment(-10.0, -2.0)])

    def test_negative_knee_rejected(self):
        with self.assertRaises(ValueError):
            compile_curve([Segment(-10.0, 2.0, knee_db=-1.0)])

    def test_overlapping_knees_rejected(self):
        with self.assertRaises(ValueError):
            compile_curve([Segment(-20.0, 2.0, knee_db=10.0),
                           Segment(-16.0, 4.0, knee_db=10.0)])

    def test_unknown_kind_rejected(self):
        with self.assertRaises(ValueError):
            compile_curve([Segment(-10.0, 2.0, kind="gate")])


if __name__ == "__main__":
    unittest.main()
