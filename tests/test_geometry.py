"""Exact-geometry unit tests: degenerate cases decided without epsilon."""

import unittest
from fractions import Fraction as F

from geometry import (
    bbox_dist2, bbox_of_segment, closest_params, eval_point, point,
)


class TestClosestParams(unittest.TestCase):
    def test_crossing_segments_distance_zero_exact_params(self):
        # Horizontal (0,0)-(3,0) crossed by vertical (1,-1)-(1,2).
        m = closest_params(point(0, 0), point(3, 0), point(1, -1), point(1, 2))
        self.assertEqual(m.dist2, 0)
        self.assertFalse(m.is_interval)
        self.assertEqual((m.s_lo, m.t_lo), (F(1, 3), F(1, 3)))
        self.assertEqual(eval_point(point(0, 0), point(3, 0), m.s_lo),
                         (F(1), F(0)))

    def test_crossing_at_rational_point(self):
        # (0,0)-(1,1) crossed by (0,1)-(1,0): intersection (1/2, 1/2).
        m = closest_params(point(0, 0), point(1, 1), point(0, 1), point(1, 0))
        self.assertEqual(m.dist2, 0)
        self.assertEqual((m.s_lo, m.t_lo), (F(1, 2), F(1, 2)))

    def test_parallel_equidistant_interval(self):
        # (0,0)-(4,0) below, (1,1)-(3,1) above: distance 1, projections
        # overlap on x in [1,3] -> s in [1/4, 3/4], t in [0,1].
        m = closest_params(point(0, 0), point(4, 0), point(1, 1), point(3, 1))
        self.assertEqual(m.dist2, 1)
        self.assertTrue(m.is_interval)
        self.assertEqual((m.s_lo, m.s_hi), (F(1, 4), F(3, 4)))
        self.assertEqual((m.t_lo, m.t_hi), (F(0), F(1)))

    def test_parallel_equidistant_single_projection_point(self):
        # (0,0)-(2,0) and (3,1)-(5,1): disjoint projections, unique pair
        # of endpoints (2,0) and (3,1).
        m = closest_params(point(0, 0), point(2, 0), point(3, 1), point(5, 1))
        self.assertEqual(m.dist2, 2)
        self.assertFalse(m.is_interval)
        self.assertEqual((m.s_lo, m.t_lo), (F(1), F(0)))

    def test_parallel_antiparallel_orientation(self):
        # Same geometry but the second segment is reversed.
        m = closest_params(point(0, 0), point(4, 0), point(3, 1), point(1, 1))
        self.assertEqual(m.dist2, 1)
        self.assertTrue(m.is_interval)
        # s = 3/4 - t/2: t in [0,1] -> s in [1/4, 3/4]; canonical order
        # puts the lexicographically smaller (s, t) first.
        self.assertEqual((m.s_lo, m.t_lo), (F(1, 4), F(1)))
        self.assertEqual((m.s_hi, m.t_hi), (F(3, 4), F(0)))

    def test_collinear_overlap_distance_zero_interval(self):
        # Overlapping collinear segments: distance 0 over the overlap.
        m = closest_params(point(0, 0), point(4, 0), point(2, 0), point(6, 0))
        self.assertEqual(m.dist2, 0)
        self.assertTrue(m.is_interval)
        self.assertEqual((m.s_lo, m.s_hi), (F(1, 2), F(1)))
        self.assertEqual((m.t_lo, m.t_hi), (F(0), F(1, 2)))

    def test_zero_length_segment_is_point(self):
        m = closest_params(point(3, 4), point(3, 4), point(0, 0), point(0, 2))
        self.assertEqual(m.dist2, 13)  # (3,4) to (0,2): 9+4
        self.assertEqual((m.s_lo, m.t_lo), (F(0), F(1)))

    def test_two_zero_length_segments(self):
        m = closest_params(point(1, 1), point(1, 1), point(4, 5), point(4, 5))
        self.assertEqual(m.dist2, 25)
        self.assertEqual((m.s_lo, m.t_lo), (F(0), F(0)))

    def test_shared_endpoint_touching_distance_zero(self):
        m = closest_params(point(0, 0), point(2, 0), point(2, 0), point(2, 3))
        self.assertEqual(m.dist2, 0)
        self.assertEqual((m.s_lo, m.t_lo), (F(1), F(0)))

    def test_rational_coordinates_exact(self):
        # Point (1/3, 1/3) to segment (0,0)-(1,0): dist2 = 1/9 exactly.
        m = closest_params(point(F(1, 3), F(1, 3)), point(F(1, 3), F(1, 3)),
                           point(0, 0), point(1, 0))
        self.assertEqual(m.dist2, F(1, 9))
        self.assertEqual(m.t_lo, F(1, 3))

    def test_endpoint_clamping(self):
        # Closest point of (5,1) on segment (0,0)-(2,0) is endpoint (2,0).
        m = closest_params(point(5, 1), point(5, 1), point(0, 0), point(2, 0))
        self.assertEqual(m.dist2, 10)
        self.assertEqual(m.t_lo, F(1))

    def test_minimizer_canonical_order(self):
        m = closest_params(point(0, 0), point(4, 0), point(1, 1), point(3, 1))
        self.assertLessEqual((m.s_lo, m.t_lo), (m.s_hi, m.t_hi))


class TestBBox(unittest.TestCase):
    def test_bbox_dist2_exact(self):
        b1 = bbox_of_segment(point(0, 0), point(1, 1))
        b2 = bbox_of_segment(point(F(3, 2), 2), point(4, 5))
        # dx = 1/2, dy = 1 -> 1/4 + 1 = 5/4
        self.assertEqual(bbox_dist2(b1, b2), F(5, 4))

    def test_bbox_overlap_zero(self):
        b1 = bbox_of_segment(point(0, 0), point(2, 2))
        b2 = bbox_of_segment(point(1, 1), point(3, 3))
        self.assertEqual(bbox_dist2(b1, b2), 0)


if __name__ == "__main__":
    unittest.main()
