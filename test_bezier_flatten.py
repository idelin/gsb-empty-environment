import unittest
from fractions import Fraction

from bezier_flatten import (
    flatten,
    flatten_cubic,
    flatten_quadratic,
)


def eval_bezier(ctrl, t):
    """Exact de Casteljau evaluation at rational t."""
    pts = [tuple(map(Fraction, p)) for p in ctrl]
    t = Fraction(t)
    while len(pts) > 1:
        pts = [
            ((1 - t) * a[0] + t * b[0], (1 - t) * a[1] + t * b[1])
            for a, b in zip(pts, pts[1:])
        ]
    return pts[0]


def dist_sq_point_segment(p, a, b):
    """Exact squared distance from point p to segment ab (Fractions)."""
    abx, aby = b[0] - a[0], b[1] - a[1]
    apx, apy = p[0] - a[0], p[1] - a[1]
    denom = abx * abx + aby * aby
    if denom == 0:
        return apx * apx + apy * apy
    s = (apx * abx + apy * aby) / denom
    if s < 0:
        s = Fraction(0)
    elif s > 1:
        s = Fraction(1)
    cx, cy = a[0] + s * abx - p[0], a[1] + s * aby - p[1]
    return cx * cx + cy * cy


def assert_within_tolerance(testcase, ctrl, result, tol, samples=201):
    """Exact check: sampled curve points stay within tol of the polyline."""
    tol = Fraction(tol)
    tol_sq = tol * tol
    for i in range(samples):
        t = Fraction(i, samples - 1)
        pt = eval_bezier(ctrl, t)
        best = min(
            dist_sq_point_segment(pt, a, b) for a, b in result.segments
        )
        testcase.assertLessEqual(
            best, tol_sq, f"curve point at t={t} violates tolerance"
        )


class TestStraightLines(unittest.TestCase):
    def test_quadratic_collinear_single_segment(self):
        r = flatten_quadratic((0, 0), (1, 1), (4, 4), Fraction(1, 1000))
        self.assertTrue(r.converged)
        self.assertEqual(len(r.points), 2)
        self.assertEqual(r.parameters, (Fraction(0), Fraction(1)))

    def test_cubic_collinear_single_segment(self):
        # Collinear but unevenly spaced control points: still one segment.
        r = flatten_cubic((0, 0), (1, 2), (3, 6), (4, 8), Fraction(1, 1000))
        self.assertTrue(r.converged)
        self.assertEqual(len(r.points), 2)

    def test_collinear_bound_is_exactly_zero(self):
        # Bound is exactly zero, so even a tiny tolerance finishes in one step.
        r = flatten_cubic((0, 0), (5, 5), (2, 2), (7, 7), Fraction(1, 10**9))
        self.assertTrue(r.converged)
        self.assertEqual(len(r.points), 2)

    def test_collinear_overshoot_subdivides(self):
        # Control points are collinear, but the curve overshoots the chord
        # (reaches (3,3) while the chord ends at (2,2)): one segment would
        # NOT cover the curve, so subdivision is required. Defined behavior.
        r = flatten_quadratic((0, 0), (5, 5), (2, 2), Fraction(1, 100))
        self.assertTrue(r.converged)
        self.assertGreater(len(r.points), 2)
        assert_within_tolerance(self, ((0, 0), (5, 5), (2, 2)), r,
                                Fraction(1, 100))


class TestKnownSubdivision(unittest.TestCase):
    def test_quadratic_exact_segment_count(self):
        # Hand-verified with the certified bound: splits exactly twice,
        # parameter knots at 0, 1/4, 1/2, 3/4, 1.
        r = flatten_quadratic((0, 0), (0, 1), (1, 1), Fraction(1, 8))
        self.assertTrue(r.converged)
        self.assertEqual(
            r.parameters,
            tuple(Fraction(i, 4) for i in range(5)),
        )
        self.assertEqual(r.points[0], (Fraction(0), Fraction(0)))
        self.assertEqual(r.points[-1], (Fraction(1), Fraction(1)))

    def test_exact_boundary_tolerance(self):
        # For (0,0),(0,1),(3,0) the certified bound squared is exactly 1:
        # tol=1 must NOT subdivide (<= comparison, no epsilon slack),
        # tol=9/10 must subdivide.
        flat = flatten_quadratic((0, 0), (0, 1), (3, 0), Fraction(1))
        self.assertEqual(len(flat.points), 2)
        not_flat = flatten_quadratic((0, 0), (0, 1), (3, 0), Fraction(9, 10))
        self.assertGreater(len(not_flat.points), 2)


class TestStructure(unittest.TestCase):
    def setUp(self):
        self.ctrl = ((0, 0), (1, 3), (4, -2), (6, 1))
        self.r = flatten_cubic(*self.ctrl, Fraction(1, 20))

    def test_parameters_cover_unit_interval(self):
        self.assertEqual(self.r.parameters[0], 0)
        self.assertEqual(self.r.parameters[-1], 1)
        for (a, b) in self.r.intervals:
            self.assertLess(a, b)
        # contiguous: end of one interval == start of the next
        for (a1, b1), (a2, b2) in zip(self.r.intervals, self.r.intervals[1:]):
            self.assertEqual(b1, a2)

    def test_endpoint_continuity(self):
        for (a1, b1), (a2, b2) in zip(self.r.segments, self.r.segments[1:]):
            self.assertEqual(b1, a2)

    def test_endpoints_match_curve(self):
        self.assertEqual(self.r.points[0], (Fraction(0), Fraction(0)))
        self.assertEqual(self.r.points[-1], (Fraction(6), Fraction(1)))

    def test_polyline_vertices_lie_on_curve(self):
        for p, t in zip(self.r.points, self.r.parameters):
            self.assertEqual(p, eval_bezier(self.ctrl, t))

    def test_dyadic_parameters(self):
        for t in self.r.parameters:
            self.assertEqual(t.denominator & (t.denominator - 1), 0)


class TestErrorGuarantee(unittest.TestCase):
    def test_quadratic_within_tolerance(self):
        ctrl = ((0, 0), (3, 5), (8, 1))
        tol = Fraction(1, 10)
        r = flatten_quadratic(*ctrl, tol)
        self.assertTrue(r.converged)
        assert_within_tolerance(self, ctrl, r, tol)

    def test_cubic_within_tolerance(self):
        ctrl = ((0, 0), (1, 4), (5, -3), (6, 2))
        tol = Fraction(1, 50)
        r = flatten_cubic(*ctrl, tol)
        self.assertTrue(r.converged)
        assert_within_tolerance(self, ctrl, r, tol)

    def test_cusp_curve_within_tolerance(self):
        ctrl = TestCusp.CUSP
        tol = Fraction(1, 100)
        r = flatten_cubic(*ctrl, tol)
        self.assertTrue(r.converged)
        assert_within_tolerance(self, ctrl, r, tol)


class TestCusp(unittest.TestCase):
    # Cubic with a genuine cusp at t = 1/3 (a non-dyadic parameter, so
    # bisection can never "accidentally" land on it):
    #   B'(1/3) = 0  because  3*P2 - 4*P0 + P3 == 0,
    #   B''(1/3) = (-18, -12) != 0.
    CUSP = ((0, 0), (2, 1), (1, 1), (-3, -3))
    CUSP_T = Fraction(1, 3)

    def test_first_derivative_vanishes(self):
        p0, p1, p2, p3 = map(lambda p: tuple(map(Fraction, p)), self.CUSP)
        # B'(1/3) is proportional to 3*P2 - 4*P0 + P3.
        dx = 3 * p2[0] - 4 * p0[0] + p3[0]
        dy = 3 * p2[1] - 4 * p0[1] + p3[1]
        self.assertEqual((dx, dy), (0, 0))

    def test_cusp_keeps_subdividing(self):
        r = flatten_cubic(*self.CUSP, Fraction(1, 500))
        self.assertTrue(r.converged)
        widths = [b - a for a, b in r.intervals]
        cusp_width = next(
            w for (a, b), w in zip(r.intervals, widths)
            if a < self.CUSP_T < b
        )
        # The interval containing the cusp is among the finest, and strictly
        # finer than the coarsest interval: subdivision continues at the cusp.
        self.assertEqual(cusp_width, min(widths))
        self.assertLess(cusp_width, max(widths))
        self.assertGreaterEqual(cusp_width.denominator, 32)


class TestDegenerate(unittest.TestCase):
    def test_all_points_coincident(self):
        r = flatten_cubic((2, 3), (2, 3), (2, 3), (2, 3), Fraction(1, 10))
        self.assertTrue(r.converged)
        self.assertEqual(r.points, ((Fraction(2), Fraction(3)),
                                    (Fraction(2), Fraction(3))))

    def test_degenerate_chord_closed_curve(self):
        # P0 == P3: chord degenerates to a point; bound falls back to
        # max |P_i - P_0|. Still converges and stays within tolerance.
        ctrl = ((0, 0), (3, 3), (-3, 3), (0, 0))
        r = flatten_cubic(*ctrl, Fraction(1, 10))
        self.assertTrue(r.converged)
        self.assertEqual(r.points[0], r.points[-1])
        assert_within_tolerance(self, ctrl, r, Fraction(1, 10))

    def test_repeated_control_points(self):
        ctrl = ((0, 0), (0, 0), (1, 1), (1, 1))
        r = flatten_cubic(*ctrl, Fraction(1, 10))
        self.assertTrue(r.converged)
        assert_within_tolerance(self, ctrl, r, Fraction(1, 10))


class TestDepthCap(unittest.TestCase):
    def test_depth_cap_reports_unmet(self):
        ctrl = ((0, 0), (0, 4), (4, 0), (4, 4))
        r = flatten_cubic(*ctrl, Fraction(1, 1000), max_depth=2)
        self.assertFalse(r.converged)
        self.assertTrue(r.unmet_intervals)
        # Unmet intervals are exactly the leaves at the depth cap.
        self.assertEqual(
            r.unmet_intervals,
            tuple((Fraction(i, 4), Fraction(i + 1, 4)) for i in range(4)),
        )
        # Polyline is still returned, continuous, and spans [0, 1].
        self.assertEqual(r.parameters[0], 0)
        self.assertEqual(r.parameters[-1], 1)
        for (a1, b1), (a2, b2) in zip(r.segments, r.segments[1:]):
            self.assertEqual(b1, a2)

    def test_max_depth_zero_nonflat(self):
        r = flatten_quadratic((0, 0), (1, 2), (2, 0), Fraction(1, 10),
                              max_depth=0)
        self.assertFalse(r.converged)
        self.assertEqual(r.unmet_intervals, ((Fraction(0), Fraction(1)),))
        self.assertEqual(len(r.points), 2)

    def test_converges_before_cap(self):
        r = flatten_quadratic((0, 0), (1, 2), (2, 0), Fraction(1, 2),
                              max_depth=100)
        self.assertTrue(r.converged)
        self.assertEqual(r.unmet_intervals, ())


class TestInputHandling(unittest.TestCase):
    def test_integer_coordinates_stay_exact(self):
        r = flatten_quadratic((0, 0), (1, 1), (2, 0), Fraction(1, 4))
        for p in r.points:
            for c in p:
                self.assertIsInstance(c, Fraction)

    def test_rational_and_mixed_input(self):
        r = flatten_cubic((0, 0), (Fraction(1, 3), 1), ("0.5", 2), (1, 0),
                          Fraction(1, 10))
        self.assertTrue(r.converged)
        self.assertEqual(r.points[0], (Fraction(0), Fraction(0)))

    def test_float_tolerance_accepted(self):
        r = flatten_quadratic((0, 0), (0, 1), (1, 1), 0.25)
        self.assertTrue(r.converged)

    def test_bad_tolerance_rejected(self):
        for bad in (0, -1, Fraction(-1, 3)):
            with self.assertRaises(ValueError):
                flatten_quadratic((0, 0), (1, 1), (2, 0), bad)

    def test_bad_control_point_count(self):
        with self.assertRaises(ValueError):
            flatten([(0, 0), (1, 1)], Fraction(1, 2))
        with self.assertRaises(ValueError):
            flatten([(0, 0)] * 5, Fraction(1, 2))

    def test_bad_max_depth(self):
        with self.assertRaises(ValueError):
            flatten_quadratic((0, 0), (1, 1), (2, 0), 1, max_depth=-1)
        with self.assertRaises(TypeError):
            flatten_quadratic((0, 0), (1, 1), (2, 0), 1, max_depth=2.5)

    def test_non_numeric_coordinate(self):
        with self.assertRaises(TypeError):
            flatten_quadratic((0, 0), ("a", 1), (2, 0), 1)


if __name__ == "__main__":
    unittest.main()
