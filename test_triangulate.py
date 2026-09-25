import unittest
from fractions import Fraction

from geom import (INSIDE, OUTSIDE, BOUNDARY, orient, point_in_ring,
                  signed_area2)
from triangulate import triangulate, PolygonError
from validate import validate_triangulation, ValidationError

SQ = [(0, 0), (4, 0), (4, 4), (0, 4)]
HOLE = [(1, 1), (3, 1), (3, 3), (1, 3)]


class TestGeom(unittest.TestCase):
    def test_orient_signs(self):
        self.assertGreater(orient((0, 0), (1, 0), (0, 1)), 0)
        self.assertLess(orient((0, 0), (0, 1), (1, 0)), 0)
        self.assertEqual(orient((0, 0), (1, 1), (2, 2)), 0)

    def test_point_in_ring_exact(self):
        self.assertEqual(point_in_ring((2, 2), SQ), INSIDE)
        self.assertEqual(point_in_ring((5, 2), SQ), OUTSIDE)
        self.assertEqual(point_in_ring((0, 2), SQ), BOUNDARY)
        self.assertEqual(point_in_ring((4, 4), SQ), BOUNDARY)
        # Rational point exactly on a rational edge.
        e = [(0, 0), (Fraction(7, 2), Fraction(7, 2)), (7, 0)]
        self.assertEqual(
            point_in_ring((Fraction(7, 4), Fraction(7, 4)), e), BOUNDARY)

    def test_signed_area(self):
        self.assertEqual(signed_area2(SQ), 32)
        self.assertEqual(signed_area2(list(reversed(SQ))), -32)


class TestTriangulateSimple(unittest.TestCase):
    def test_square_two_triangles(self):
        tris = triangulate(SQ)
        self.assertEqual(len(tris), 2)
        self.assertEqual(sum(t.area for t in tris), 16)
        self.assertTrue(validate_triangulation(SQ, [], tris))

    def test_clockwise_input_normalized(self):
        tris = triangulate(list(reversed(SQ)))
        self.assertEqual(len(tris), 2)
        self.assertTrue(validate_triangulation(SQ, [], tris))
        for t in tris:
            self.assertGreater(t.area2, 0)

    def test_collinear_vertices_no_zero_area_output(self):
        outer = [(0, 0), (2, 0), (4, 0), (4, 4), (2, 4), (0, 4)]
        tris = triangulate(outer)
        self.assertTrue(all(t.area2 > 0 for t in tris))
        self.assertEqual(sum(t.area for t in tris), 16)
        self.assertTrue(validate_triangulation(outer, [], tris))

    def test_consecutive_duplicates_dropped(self):
        outer = [(0, 0), (0, 0), (4, 0), (4, 4), (0, 4), (0, 4)]
        tris = triangulate(outer)
        self.assertEqual(len(tris), 2)
        self.assertTrue(validate_triangulation(SQ, [], tris))

    def test_rational_coordinates_exact_area(self):
        outer = [(0, 0), (Fraction(7, 2), 0),
                 (Fraction(7, 2), Fraction(5, 3)), (0, Fraction(5, 3))]
        tris = triangulate(outer)
        self.assertEqual(sum(t.area for t in tris),
                         Fraction(7, 2) * Fraction(5, 3))
        self.assertTrue(validate_triangulation(outer, [], tris))

    def test_provenance_references_outer_ring(self):
        tris = triangulate(SQ)
        for t in tris:
            self.assertEqual(len(t.source), 3)
            for ring, idx in t.source:
                self.assertEqual(ring, 0)
                self.assertIn(idx, range(4))


class TestDegenerateInput(unittest.TestCase):
    def test_too_few_vertices(self):
        with self.assertRaises(PolygonError):
            triangulate([(0, 0), (1, 1)])

    def test_zero_area_ring(self):
        with self.assertRaises(PolygonError):
            triangulate([(0, 0), (1, 1), (2, 2), (3, 3)])

    def test_repeated_vertex(self):
        with self.assertRaises(PolygonError):
            triangulate([(0, 0), (4, 0), (4, 4), (0, 4), (4, 0)])

    def test_self_intersection(self):
        with self.assertRaises(PolygonError):
            triangulate([(0, 0), (4, 4), (4, 0), (0, 4)])

    def test_spike_rejected(self):
        with self.assertRaises(PolygonError):
            triangulate([(0, 0), (4, 0), (2, 0), (0, 4)])

    def test_zero_area_hole(self):
        with self.assertRaises(PolygonError):
            triangulate(SQ, [[(1, 1), (2, 2), (3, 3)]])


class TestHoles(unittest.TestCase):
    def test_square_with_square_hole(self):
        tris = triangulate(SQ, [HOLE])
        # n_total + 2*h - 2 = 8 + 2 - 2 triangles, no collinear removals.
        self.assertEqual(len(tris), 8)
        self.assertEqual(sum(t.area for t in tris), 16 - 4)
        self.assertTrue(validate_triangulation(SQ, [HOLE], tris))

    def test_hole_provenance(self):
        tris = triangulate(SQ, [HOLE])
        rings_used = {r for t in tris for r, _ in t.source}
        self.assertEqual(rings_used, {0, 1})
        for t in tris:
            for ring, idx in t.source:
                self.assertIn(ring, (0, 1))
                self.assertIn(idx, range(4))

    def test_two_holes(self):
        hole2 = [(Fraction(1, 4), Fraction(7, 2)), (Fraction(3, 4), Fraction(7, 2)),
                 (Fraction(3, 4), Fraction(15, 4)), (Fraction(1, 4), Fraction(15, 4))]
        tris = triangulate(SQ, [HOLE, hole2])
        self.assertEqual(sum(t.area for t in tris), 16 - 4 - Fraction(1, 8))
        self.assertTrue(validate_triangulation(SQ, [HOLE, hole2], tris))

    def test_wide_hole_forces_long_bridge(self):
        # Hole spanning most of the width: bridge endpoint choice matters.
        outer = [(0, 0), (10, 0), (10, 10), (0, 10)]
        hole = [(2, 3), (8, 3), (8, 5), (2, 5)]
        tris = triangulate(outer, [hole])
        self.assertEqual(sum(t.area for t in tris), 100 - 12)
        self.assertTrue(validate_triangulation(outer, [hole], tris))
        self.assertTrue(all(t.area2 > 0 for t in tris))

    def test_hole_with_collinear_vertices(self):
        outer = [(0, 0), (10, 0), (10, 10), (0, 10)]
        hole = [(2, 2), (4, 2), (6, 2), (6, 4), (2, 4)]
        tris = triangulate(outer, [hole])
        self.assertTrue(all(t.area2 > 0 for t in tris))
        self.assertEqual(sum(t.area for t in tris), 100 - 8)
        self.assertTrue(validate_triangulation(outer, [hole], tris))

    def test_rational_hole_exact_area(self):
        outer = [(0, 0), (5, 0), (5, 5), (0, 5)]
        hole = [(Fraction(3, 2), Fraction(3, 2)), (Fraction(7, 2), Fraction(3, 2)),
                (Fraction(7, 2), Fraction(5, 2)), (Fraction(3, 2), Fraction(5, 2))]
        tris = triangulate(outer, [hole])
        self.assertEqual(sum(t.area for t in tris), 25 - 2)
        self.assertTrue(validate_triangulation(outer, [hole], tris))

    def test_hole_outside_outer_rejected(self):
        with self.assertRaises(PolygonError):
            triangulate(SQ, [[(10, 10), (12, 10), (12, 12), (10, 12)]])

    def test_hole_touching_outer_rejected(self):
        with self.assertRaises(PolygonError):
            triangulate(SQ, [[(0, 1), (2, 1), (2, 3), (0, 3)]])

    def test_nested_holes_rejected(self):
        with self.assertRaises(PolygonError):
            triangulate(SQ, [HOLE, [(Fraction(3, 2), Fraction(3, 2)),
                                    (Fraction(5, 2), Fraction(3, 2)),
                                    (Fraction(5, 2), Fraction(5, 2)),
                                    (Fraction(3, 2), Fraction(5, 2))]])

    def test_touching_holes_rejected(self):
        with self.assertRaises(PolygonError):
            triangulate([(0, 0), (8, 0), (8, 4), (0, 4)],
                        [[(1, 1), (3, 1), (3, 3), (1, 3)],
                         [(3, 1), (5, 1), (5, 3), (3, 3)]])


class TestValidator(unittest.TestCase):
    def setUp(self):
        self.tris = triangulate(SQ, [HOLE])

    def test_area_tampering_detected(self):
        bad = list(self.tris)
        t = bad[0]
        moved = ((t.points[0][0] + 1, t.points[0][1]),) + t.points[1:]
        bad[0] = type(t)(points=moved, source=t.source)
        with self.assertRaises(ValidationError):
            validate_triangulation(SQ, [HOLE], bad)

    def test_clockwise_triangle_detected(self):
        t = self.tris[0]
        bad = [type(t)(points=(t.points[0], t.points[2], t.points[1]),
                       source=t.source)] + self.tris[1:]
        with self.assertRaises(ValidationError):
            validate_triangulation(SQ, [HOLE], bad)

    def test_triangle_inside_hole_detected(self):
        t = self.tris[0]
        inside = ((Fraction(3, 2), Fraction(3, 2)), (Fraction(5, 2), Fraction(3, 2)),
                  (Fraction(3, 2), Fraction(5, 2)))
        bad = self.tris + [type(t)(points=inside, source=t.source)]
        with self.assertRaises(ValidationError):
            validate_triangulation(SQ, [HOLE], bad)

    def test_overlapping_triangle_detected(self):
        t = self.tris[0]
        bad = self.tris + [t]
        with self.assertRaises(ValidationError):
            validate_triangulation(SQ, [HOLE], bad)

    def test_missing_triangle_detected(self):
        with self.assertRaises(ValidationError):
            validate_triangulation(SQ, [HOLE], self.tris[:-1])


if __name__ == "__main__":
    unittest.main()
