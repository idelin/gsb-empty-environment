import unittest
from fractions import Fraction as F

from polytri.exact import area2, to_point
from polytri.preprocess import PolygonError, clean_ring, normalize_polygon


class TestCleanRing(unittest.TestCase):
    def test_removes_consecutive_duplicates(self):
        ring = clean_ring([(0, 0), (0, 0), (4, 0), (4, 4), (0, 4), (0, 0)])
        self.assertEqual(len(ring), 4)

    def test_removes_collinear_vertices(self):
        ring = clean_ring([(0, 0), (2, 0), (4, 0), (4, 4), (0, 4)])
        self.assertEqual(ring, [to_point(p) for p in [(0, 0), (4, 0), (4, 4), (0, 4)]])

    def test_removes_rational_collinear_vertex(self):
        ring = clean_ring([(0, 0), (F(7, 2), F(7, 4)), (7, F(7, 2)), (7, 7), (0, 7)])
        self.assertEqual(len(ring), 4)

    def test_keeps_region_unchanged(self):
        before = [to_point(p) for p in [(0, 0), (2, 0), (4, 0), (4, 4), (0, 4)]]
        after = clean_ring(before)
        self.assertEqual(area2(before), area2(after))


class TestNormalize(unittest.TestCase):
    def test_orientation_is_normalised(self):
        outer, holes = normalize_polygon(
            [(0, 0), (0, 10), (10, 10), (10, 0)],  # CW on input
            [[(3, 3), (3, 7), (7, 7), (7, 3)]],    # CW on input
        )
        self.assertGreater(area2(outer), 0)
        self.assertLess(area2(holes[0]), 0)

    def test_bowtie_rejected(self):
        with self.assertRaises(PolygonError):
            normalize_polygon([(0, 0), (4, 4), (4, 0), (0, 4)])

    def test_duplicate_vertex_rejected(self):
        with self.assertRaises(PolygonError):
            normalize_polygon([(0, 0), (6, 0), (6, 6), (3, 3), (0, 6), (3, 3)])

    def test_spike_rejected(self):
        with self.assertRaises(PolygonError):
            normalize_polygon([(0, 0), (4, 0), (2, 0), (4, 4), (0, 4)])

    def test_zero_area_outer_rejected(self):
        with self.assertRaises(PolygonError):
            normalize_polygon([(0, 0), (2, 2), (4, 4)])

    def test_zero_area_hole_rejected(self):
        with self.assertRaises(PolygonError):
            normalize_polygon(
                [(0, 0), (10, 0), (10, 10), (0, 10)],
                [[(3, 3), (5, 5), (7, 7)]],
            )

    def test_hole_outside_rejected(self):
        with self.assertRaises(PolygonError):
            normalize_polygon(
                [(0, 0), (10, 0), (10, 10), (0, 10)],
                [[(20, 20), (22, 20), (22, 22)]],
            )

    def test_hole_touching_outer_rejected(self):
        with self.assertRaises(PolygonError):
            normalize_polygon(
                [(0, 0), (10, 0), (10, 10), (0, 10)],
                [[(10, 5), (8, 3), (8, 7)]],
            )

    def test_touching_holes_rejected(self):
        with self.assertRaises(PolygonError):
            normalize_polygon(
                [(0, 0), (10, 0), (10, 10), (0, 10)],
                [
                    [(2, 2), (5, 2), (5, 5), (2, 5)],
                    [(5, 3), (8, 3), (8, 6), (5, 6)],
                ],
            )

    def test_nested_holes_rejected(self):
        with self.assertRaises(PolygonError):
            normalize_polygon(
                [(0, 0), (10, 0), (10, 10), (0, 10)],
                [
                    [(2, 2), (8, 2), (8, 8), (2, 8)],
                    [(4, 4), (6, 4), (6, 6), (4, 6)],
                ],
            )


if __name__ == "__main__":
    unittest.main()
