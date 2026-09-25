import unittest
from fractions import Fraction as F

from polytri import triangulate, validate_triangulation
from polytri.preprocess import normalize_polygon


def check_valid(self, outer, holes):
    tris = triangulate(outer, holes)
    report = validate_triangulation(outer, holes, tris)
    self.assertTrue(report.ok, msg="\n" + report.summary())
    return tris, report


class TestTriangulate(unittest.TestCase):
    def test_square_no_hole(self):
        tris, report = check_valid(self, [(0, 0), (10, 0), (10, 10), (0, 10)], [])
        self.assertEqual(len(tris), 2)
        self.assertEqual(report.triangle_area, F(100))

    def test_square_with_square_hole(self):
        outer = [(0, 0), (10, 0), (10, 10), (0, 10)]
        hole = [(3, 3), (7, 3), (7, 7), (3, 7)]
        tris, report = check_valid(self, outer, [hole])
        # V = 8 vertices, H = 1 hole  =>  V + 2H - 2 = 8 triangles
        self.assertEqual(len(tris), 8)
        self.assertEqual(report.triangle_area, F(84))
        # bridge triangles must reference both rings
        self.assertTrue(any(t.sources == ("hole[0]", "outer") for t in tris))

    def test_triangle_count_formula(self):
        outer = [(0, 0), (12, 0), (14, 6), (12, 12), (0, 12)]
        holes = [
            [(2, 2), (6, 2), (6, 4), (2, 4)],
            [(8, 8), (11, 8), (11, 10), (8, 10)],
        ]
        tris, _ = check_valid(self, outer, holes)
        outer_ring, hole_rings = normalize_polygon(outer, holes)
        v = len(outer_ring) + sum(len(h) for h in hole_rings)
        self.assertEqual(len(tris), v + 2 * len(holes) - 2)

    def test_no_zero_area_output(self):
        outer = [(0, 0), (10, 0), (10, 10), (0, 10)]
        hole = [(3, 3), (7, 3), (7, 7), (3, 7)]
        tris, _ = check_valid(self, outer, [hole])
        for t in tris:
            self.assertGreater(t.area, 0)
            self.assertEqual(len(set(t.vertices)), 3)

    def test_rational_coordinates(self):
        outer = [(0, 0), (7, 0), (7, 5), (0, 5)]
        hole = [(1, 1), (2, 1), (F(3, 2), F(3, 2))]
        tris, report = check_valid(self, outer, [hole])
        self.assertEqual(report.triangle_area, F(35) - F(1, 4))
        self.assertEqual(report.region_area, F(139, 4))

    def test_collinear_vertices_on_both_rings(self):
        outer = [(0, 0), (5, 0), (10, 0), (10, 5), (10, 10), (0, 10)]
        hole = [(3, 3), (5, 3), (7, 3), (7, 7), (3, 7)]
        tris, report = check_valid(self, outer, [hole])
        self.assertEqual(report.triangle_area, F(100) - F(16))
        # the redundant collinear vertices are never used as corners
        used = {p for t in tris for p in t.vertices}
        self.assertNotIn((F(5), F(0)), used)
        self.assertNotIn((F(5), F(3)), used)

    def test_bridge_ray_hits_vertex_exactly(self):
        # the horizontal ray from the hole's rightmost vertex (6, 6) meets
        # the outer ring exactly at the vertex (14, 6)
        outer = [(0, 0), (12, 0), (14, 6), (12, 12), (0, 12)]
        hole = [(4, 4), (6, 6), (4, 8)]
        tris, report = check_valid(self, outer, [hole])
        self.assertEqual(report.triangle_area, report.region_area)

    def test_concave_outer_with_two_holes(self):
        outer = [(0, 0), (12, 0), (12, 4), (6, 4), (6, 8), (12, 8), (12, 12), (0, 12)]
        holes = [
            [(8, 1), (11, 1), (11, 3), (8, 3)],
            [(1, 5), (4, 5), (4, 10), (1, 10)],
        ]
        tris, report = check_valid(self, outer, holes)
        labels = {t.source_label for t in tris}
        self.assertTrue(any("hole[0]" in s for s in labels))
        self.assertTrue(any("hole[1]" in s for s in labels))

    def test_bridge_must_avoid_other_hole(self):
        # the second hole sits exactly on the straight ray from the first
        # hole's rightmost vertex, so the bridge must dodge it
        outer = [(0, 0), (20, 0), (20, 10), (0, 10)]
        holes = [
            [(3, 3), (6, 3), (6, 7), (3, 7)],
            [(8, 4), (18, 4), (18, 6), (8, 6)],
        ]
        check_valid(self, outer, holes)

    def test_deterministic_output(self):
        outer = [(0, 0), (12, 0), (14, 6), (12, 12), (0, 12)]
        holes = [[(4, 4), (6, 6), (4, 8)]]
        first = triangulate(outer, holes)
        second = triangulate(outer, holes)
        self.assertEqual(first, second)

    def test_orientation_of_input_is_irrelevant(self):
        outer_ccw = [(0, 0), (10, 0), (10, 10), (0, 10)]
        outer_cw = list(reversed(outer_ccw))
        hole = [(3, 3), (7, 3), (7, 7), (3, 7)]
        t1 = triangulate(outer_ccw, [hole])
        t2 = triangulate(outer_cw, [list(reversed(hole))])
        self.assertEqual(t1, t2)


if __name__ == "__main__":
    unittest.main()
