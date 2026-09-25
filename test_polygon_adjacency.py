import math
import unittest
from fractions import Fraction as F

from polygon_adjacency import analyze, snap_vertices, normalize_polygon


def square(x0, y0, x1, y1):
    return [(x0, y0), (x1, y0), (x1, y1), (x0, y1)]


class SharedEdgeTests(unittest.TestCase):
    def test_full_shared_edge(self):
        a = square(0, 0, 1, 1)
        b = square(1, 0, 2, 1)
        r = analyze(a, b)
        self.assertEqual(len(r.shared_edges), 1)
        e = r.shared_edges[0]
        self.assertEqual(e.length_squared, F(1))
        self.assertEqual({e.start, e.end}, {(F(1), F(0)), (F(1), F(1))})
        # endpoints of a shared edge are not reported as point contacts
        self.assertEqual(r.point_contacts, [])
        self.assertEqual(r.snap_records, [])

    def test_partial_overlap_exact_rational_length(self):
        a = square(0, 0, 1, 1)
        b = square(1, F(1, 2), 2, F(3, 2))
        r = analyze(a, b)
        self.assertEqual(len(r.shared_edges), 1)
        self.assertEqual(r.shared_edges[0].length_squared, F(1, 4))
        self.assertAlmostEqual(r.total_shared_length, 0.5)

    def test_long_edge_against_two_short_edges_aggregates(self):
        a = square(0, 0, 10, 1)
        # B sits below A; B's top edge is split into [0,4] and [4,10]
        # by a collinear vertex, both matching A's single bottom edge.
        b = [(0, 0), (4, 0), (10, 0), (10, -1), (0, -1)]
        r = analyze(a, b)
        self.assertEqual(len(r.shared_edges), 1)
        e = r.shared_edges[0]
        self.assertEqual(e.length_squared, F(100))
        self.assertEqual({e.start, e.end}, {(F(0), F(0)), (F(10), F(0))})
        self.assertEqual(r.total_shared_length, 10.0)

    def test_duplicate_edges_not_double_counted(self):
        a = square(0, -1, 4, 0)
        # degenerate chain tracing segment (0,0)-(4,0) three times
        b = [(0, 0), (4, 0), (0, 0), (0, 1), (4, 1), (4, 0)]
        r = analyze(a, b)
        self.assertEqual(len(r.shared_edges), 1)
        self.assertEqual(r.shared_edges[0].length_squared, F(16))
        self.assertEqual(r.total_shared_length, 4.0)

    def test_diagonal_shared_edge_exact_squared_length(self):
        t1 = [(0, 0), (1, 0), (1, 1)]
        t2 = [(0, 0), (0, 1), (1, 1)]
        r = analyze(t1, t2)
        self.assertEqual(len(r.shared_edges), 1)
        e = r.shared_edges[0]
        self.assertEqual(e.length_squared, F(2))  # exact, not epsilon-compared
        self.assertAlmostEqual(e.length, math.sqrt(2))

    def test_disjoint_polygons(self):
        r = analyze(square(0, 0, 1, 1), square(5, 5, 6, 6))
        self.assertEqual(r.shared_edges, [])
        self.assertEqual(r.point_contacts, [])
        self.assertEqual(r.total_shared_length, 0.0)


class PointContactTests(unittest.TestCase):
    def test_corner_touch_is_point_contact_not_edge_adjacency(self):
        a = square(0, 0, 1, 1)
        b = square(1, 1, 2, 2)
        r = analyze(a, b)
        self.assertEqual(r.shared_edges, [])
        self.assertEqual(r.point_contacts, [(F(1), F(1))])

    def test_collinear_endpoint_touch_is_point_contact(self):
        a = square(0, 0, 1, 1)
        b = square(1, -1, 2, 0)  # top edge collinear with A's bottom edge
        r = analyze(a, b)
        self.assertEqual(r.shared_edges, [])
        self.assertEqual(r.point_contacts, [(F(1), F(0))])

    def test_vertex_on_edge_interior_t_junction(self):
        a = square(0, 0, 2, 1)
        b = [(1, 1), (1, 2), (F(3, 2), F(3, 2))]  # apex touches A's top edge
        r = analyze(a, b)
        self.assertEqual(r.shared_edges, [])
        self.assertEqual(r.point_contacts, [(F(1), F(1))])

    def test_point_contact_deduplicated(self):
        # B touches A's corner with two of its own edges meeting there:
        # the contact point must appear exactly once.
        a = square(0, 0, 1, 1)
        b = square(1, 1, 2, 2)
        r = analyze(a, b)
        self.assertEqual(len(r.point_contacts), 1)


class DegenerateInputTests(unittest.TestCase):
    def test_zero_length_edges_ignored(self):
        a = square(0, 0, 1, 1)
        b = [(1, 0), (2, 0), (2, 0), (2, 1), (1, 1)]  # repeated vertex
        r = analyze(a, b)
        self.assertEqual(len(r.shared_edges), 1)
        self.assertEqual(r.shared_edges[0].length_squared, F(1))
        self.assertEqual(r.point_contacts, [])

    def test_empty_and_tiny_polygons(self):
        self.assertEqual(analyze([], square(0, 0, 1, 1)).shared_edges, [])
        self.assertEqual(analyze([(0, 0)], square(0, 0, 1, 1)).point_contacts, [])

    def test_reversed_orientation_same_result(self):
        a = square(0, 0, 1, 1)
        b = square(1, 0, 2, 1)[::-1]
        r = analyze(a, b)
        self.assertEqual(len(r.shared_edges), 1)
        self.assertEqual(r.shared_edges[0].length_squared, F(1))

    def test_invalid_coordinate_type_rejected(self):
        with self.assertRaises(TypeError):
            analyze([(0.0, 0.0), (1.0, 0.0), (1.0, 1.0)], square(0, 0, 1, 1))


class SnappingTests(unittest.TestCase):
    def setUp(self):
        self.a = square(0, 0, 1, 1)
        # B is the unit square to the right, shifted by 1/100 in x and y
        self.b = [(F(101, 100), F(1, 100)), (F(201, 100), F(1, 100)),
                  (F(201, 100), F(101, 100)), (F(101, 100), F(101, 100))]

    def test_without_tolerance_near_miss_is_exact(self):
        r = analyze(self.a, self.b)
        self.assertEqual(r.shared_edges, [])
        self.assertEqual(r.point_contacts, [])
        self.assertEqual(r.snap_records, [])

    def test_snap_reports_every_displacement(self):
        r = analyze(self.a, self.b, snap_tolerance=F(1, 10))
        # two left vertices of B snap onto A's right edge corners
        self.assertEqual(len(r.snap_records), 2)
        for rec in r.snap_records:
            self.assertEqual(rec.polygon, "B")
            self.assertEqual(rec.displacement,
                             (rec.snapped[0] - rec.original[0],
                              rec.snapped[1] - rec.original[1]))
            self.assertEqual(rec.distance_squared,
                             rec.displacement[0] ** 2 + rec.displacement[1] ** 2)
        snapped_targets = {rec.snapped for rec in r.snap_records}
        self.assertEqual(snapped_targets, {(F(1), F(0)), (F(1), F(1))})
        # after snapping the shared edge is exact
        self.assertEqual(len(r.shared_edges), 1)
        self.assertEqual(r.shared_edges[0].length_squared, F(1))

    def test_snap_leaves_unrelated_vertices_untouched(self):
        # B has one vertex near A and the rest far away
        b = [(F(101, 100), 0), (5, 0), (5, 3), (2, 3)]
        moved, records = snap_vertices(
            normalize_polygon(self.a), normalize_polygon(b), F(1, 10))
        self.assertEqual(len(records), 1)
        self.assertEqual(moved[0], (F(1), F(0)))
        self.assertEqual(moved[1:], normalize_polygon(b)[1:])

    def test_snap_does_not_change_unrelated_topology(self):
        # A far-away third shape travelling with B must not gain contacts.
        a = self.a
        b = self.b + []  # same near-miss square
        r = analyze(a, b, snap_tolerance=F(1, 1000))
        # tolerance too small: nothing snaps, nothing is invented
        self.assertEqual(r.snap_records, [])
        self.assertEqual(r.shared_edges, [])
        self.assertEqual(r.point_contacts, [])

    def test_snap_target_used_once_no_cascade(self):
        a = [(0, 0), (4, 0), (4, 4), (0, 4)]
        # two B vertices both within tolerance of A's corner (0,0):
        # only the nearer one may snap to it
        b = [(F(1, 100), 0), (F(1, 50), 0), (3, 0), (3, -1)]
        moved, records = snap_vertices(
            normalize_polygon(a), normalize_polygon(b), F(1, 10))
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0].vertex_index, 0)

    def test_negative_tolerance_rejected(self):
        with self.assertRaises(ValueError):
            analyze(self.a, self.b, snap_tolerance=F(-1))


if __name__ == "__main__":
    unittest.main(verbosity=2)
