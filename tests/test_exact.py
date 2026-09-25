"""Exact-arithmetic kernel tests: no epsilon, degenerate cases defined."""
import unittest
from fractions import Fraction

from geomesh.exact import (
    make_point,
    on_segment,
    orient,
    polygon_is_simple,
    segments_intersect,
    segments_overlap,
    signed_area2,
    strictly_on_segment,
    to_fraction,
)


class TestExactPredicates(unittest.TestCase):
    def test_orient_with_rationals(self):
        a = make_point(0, 0)
        b = make_point(Fraction(7, 3), Fraction(1, 2))
        self.assertEqual(orient(a, b, make_point(0, 1)), 1)
        self.assertEqual(orient(a, b, make_point(1, -1)), -1)
        # Exactly collinear rational point: 0 means collinear, no epsilon.
        self.assertEqual(orient(a, b, make_point(Fraction(7, 6), Fraction(1, 4))), 0)

    def test_orient_with_huge_integers(self):
        big = 10**30
        self.assertEqual(orient((0, 0), (big, big), (big + 1, big)), -1)
        self.assertEqual(orient((0, 0), (big, big), (big, big + 1)), 1)

    def test_on_segment_exact(self):
        a, b = make_point(0, 0), make_point(3, 3)
        self.assertTrue(on_segment(a, b, make_point(Fraction(1, 3), Fraction(1, 3))))
        self.assertFalse(on_segment(a, b, make_point(Fraction(1, 3), Fraction(1, 4))))
        self.assertTrue(strictly_on_segment(a, b, make_point(1, 1)))
        self.assertFalse(strictly_on_segment(a, b, a))  # endpoints excluded

    def test_segments_intersect_touching_counts(self):
        a, b = make_point(0, 0), make_point(4, 0)
        c, d = make_point(4, 0), make_point(4, 4)
        self.assertTrue(segments_intersect(a, b, c, d))  # shared endpoint
        e, f = make_point(2, -1), make_point(2, 1)
        self.assertTrue(segments_intersect(a, b, e, f))  # proper crossing
        g, h = make_point(5, 0), make_point(6, 1)
        self.assertFalse(segments_intersect(a, b, g, h))

    def test_segments_overlap_collinear(self):
        a, b = make_point(0, 0), make_point(4, 0)
        c, d = make_point(2, 0), make_point(6, 0)
        self.assertTrue(segments_overlap(a, b, c, d))
        e, f = make_point(4, 0), make_point(8, 0)
        self.assertFalse(segments_overlap(a, b, e, f))  # touch at one point only

    def test_signed_area_orientation(self):
        ccw = [make_point(*p) for p in [(0, 0), (4, 0), (4, 4), (0, 4)]]
        self.assertEqual(signed_area2(ccw), 32)
        self.assertEqual(signed_area2(list(reversed(ccw))), -32)

    def test_polygon_simplicity_degenerates(self):
        square = [make_point(*p) for p in [(0, 0), (4, 0), (4, 4), (0, 4)]]
        self.assertTrue(polygon_is_simple(square))
        bowtie = [make_point(*p) for p in [(0, 0), (4, 4), (4, 0), (0, 4)]]
        self.assertFalse(polygon_is_simple(bowtie))  # crossing edges
        spike = [make_point(*p) for p in [(0, 0), (4, 0), (2, 0), (0, 4)]]
        self.assertFalse(polygon_is_simple(spike))  # collinear overlap
        dup = [make_point(*p) for p in [(0, 0), (4, 0), (4, 0), (0, 4)]]
        self.assertFalse(polygon_is_simple(dup))  # repeated vertex
        touch = [make_point(*p) for p in [(0, 0), (4, 0), (2, 2), (4, 4), (0, 4), (2, 2)]]
        self.assertFalse(polygon_is_simple(touch))  # vertex visited twice

    def test_reject_float_coordinates(self):
        with self.assertRaises(TypeError):
            to_fraction(0.5)
        with self.assertRaises(TypeError):
            make_point(1.0, 2)
        self.assertEqual(to_fraction("3/4"), Fraction(3, 4))
        self.assertEqual(to_fraction(5), Fraction(5))


if __name__ == "__main__":
    unittest.main()
