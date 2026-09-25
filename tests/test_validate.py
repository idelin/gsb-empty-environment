import unittest
from fractions import Fraction as F

from polytri import triangulate, validate_triangulation

OUTER = [(0, 0), (10, 0), (10, 10), (0, 10)]
HOLE = [(3, 3), (7, 3), (7, 7), (3, 7)]


class TestValidate(unittest.TestCase):
    def setUp(self):
        self.tris = triangulate(OUTER, [HOLE])
        self.report = validate_triangulation(OUTER, [HOLE], self.tris)

    def test_valid_triangulation_passes(self):
        self.assertTrue(self.report.ok, msg="\n" + self.report.summary())
        self.assertEqual(self.report.triangle_area, F(84))
        self.assertEqual(self.report.region_area, F(84))

    def test_accepts_plain_point_triples(self):
        plain = [t.vertices for t in self.tris]
        report = validate_triangulation(OUTER, [HOLE], plain)
        self.assertTrue(report.ok, msg="\n" + report.summary())

    def test_missing_triangle_detected(self):
        report = validate_triangulation(OUTER, [HOLE], self.tris[:-1])
        self.assertFalse(report.ok)
        self.assertFalse(report.checks["area_conservation"])

    def test_zero_area_triangle_detected(self):
        tampered = list(self.tris) + [
            ((0, 0), (5, 5), (10, 10))  # collinear triple, e.g. from a bridge slit
        ]
        report = validate_triangulation(OUTER, [HOLE], tampered)
        self.assertFalse(report.ok)
        self.assertFalse(report.checks["non_degenerate"])

    def test_triangle_inside_hole_detected(self):
        evil = ((4, 4), (6, 4), (5, 6))  # entirely inside the hole
        report = validate_triangulation(OUTER, [HOLE], list(self.tris) + [evil])
        self.assertFalse(report.ok)
        self.assertFalse(report.checks["inside_region"])

    def test_foreign_vertex_detected(self):
        tampered = [t.vertices for t in self.tris]
        tampered[0] = (tampered[0][0], tampered[0][1], (F(1, 2), F(1, 2)))
        report = validate_triangulation(OUTER, [HOLE], tampered)
        self.assertFalse(report.ok)
        self.assertFalse(report.checks["vertices_from_input"])

    def test_overlapping_triangles_detected(self):
        tampered = list(self.tris) + [self.tris[0]]
        report = validate_triangulation(OUTER, [HOLE], tampered)
        self.assertFalse(report.ok)
        self.assertFalse(report.checks["triangles_disjoint"])

    def test_invalid_input_reported_not_raised(self):
        report = validate_triangulation([(0, 0), (4, 4), (4, 0), (0, 4)], [], [])
        self.assertFalse(report.ok)
        self.assertFalse(report.checks["input_valid"])


if __name__ == "__main__":
    unittest.main()
