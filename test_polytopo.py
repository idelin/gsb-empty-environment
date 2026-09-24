"""Unittest suite for polytopo (Python 3.11, standard library only)."""

import unittest
from fractions import Fraction

from polytopo import (
    Adjacency,
    analyze_adjacency,
    as_point,
    snap_polygon_to_polygon,
    snap_vertices,
)

F = Fraction


class SharedEdgeTests(unittest.TestCase):
    def test_full_shared_edge(self):
        a = [(0, 0), (1, 0), (1, 1), (0, 1)]
        b = [(1, 0), (2, 0), (2, 1), (1, 1)]
        adj = analyze_adjacency(a, b)
        self.assertTrue(adj.is_edge_adjacent)
        self.assertEqual(len(adj.shared_segments), 1)
        self.assertEqual(adj.shared_length, F(1))
        self.assertEqual(adj.point_contacts, ())

    def test_partial_overlap(self):
        a = [(0, 0), (2, 0), (2, 2), (0, 2)]
        b = [(2, 1), (4, 1), (4, 3), (2, 3)]
        adj = analyze_adjacency(a, b)
        self.assertTrue(adj.is_edge_adjacent)
        self.assertEqual(adj.shared_length, F(1))
        seg = adj.shared_segments[0]
        self.assertEqual(seg.start, (F(2), F(1)))
        self.assertEqual(seg.end, (F(2), F(2)))
        self.assertEqual(adj.point_contacts, ())

    def test_long_edge_against_two_short_edges(self):
        # A's right side is one edge of length 2; B's left side is split
        # into two edges of length 1 by a collinear vertex at (2, 1).
        a = [(0, 0), (2, 0), (2, 2), (0, 2)]
        b = [(2, 0), (4, 0), (4, 2), (2, 2), (2, 1)]
        adj = analyze_adjacency(a, b)
        self.assertTrue(adj.is_edge_adjacent)
        self.assertEqual(adj.shared_length, F(2))
        # Contiguous pieces merge into a single maximal segment.
        self.assertEqual(len(adj.shared_segments), 1)
        seg = adj.shared_segments[0]
        self.assertEqual(seg.start, (F(2), F(0)))
        self.assertEqual(seg.end, (F(2), F(2)))
        # The mid-vertex (2,1) is interior to the shared segment, not a
        # point contact.
        self.assertEqual(adj.point_contacts, ())

    def test_duplicate_edges_not_double_counted(self):
        a = [(0, 0), (2, 0), (2, 1), (0, 1)]
        # B's left edge (2,0)-(2,1) appears twice (retraced).
        b = [(2, 0), (4, 0), (4, 1), (2, 1), (2, 0), (2, 1)]
        adj = analyze_adjacency(a, b)
        self.assertEqual(adj.shared_length, F(1))
        self.assertEqual(len(adj.shared_segments), 1)

    def test_duplicate_edges_in_both_polygons(self):
        # Both rings retrace the shared edge; length still counted once.
        a = [(0, 0), (2, 0), (2, 1), (0, 1), (0, 0), (0, 1)]
        b = [(2, 0), (4, 0), (4, 1), (2, 1), (2, 0), (2, 1)]
        adj = analyze_adjacency(a, b)
        self.assertEqual(adj.shared_length, F(1))

    def test_zero_length_edges_dropped(self):
        a = [(0, 0), (1, 0), (1, 0), (1, 1), (0, 1)]
        b = [(1, 0), (2, 0), (2, 1), (1, 1)]
        adj = analyze_adjacency(a, b)
        self.assertEqual(adj.shared_length, F(1))
        self.assertTrue(adj.is_edge_adjacent)

    def test_identical_polygons_counted_once(self):
        a = [(0, 0), (2, 0), (2, 2), (0, 2)]
        adj = analyze_adjacency(a, a)
        self.assertEqual(adj.shared_length, F(8))
        self.assertEqual(len(adj.shared_segments), 4)

    def test_reversed_orientation_same_result(self):
        a = [(0, 0), (1, 0), (1, 1), (0, 1)]
        b = [(1, 0), (2, 0), (2, 1), (1, 1)]
        fwd = analyze_adjacency(a, b)
        rev = analyze_adjacency(a, list(reversed(b)))
        self.assertEqual(fwd.shared_length, rev.shared_length)
        self.assertEqual(fwd.shared_segments, rev.shared_segments)

    def test_rational_coordinates_exact(self):
        a = [(0, 0), (F(3, 2), 0), (F(3, 2), 1), (0, 1)]
        b = [(F(3, 2), F(1, 3)), (3, F(1, 3)), (3, 1), (F(3, 2), 1)]
        adj = analyze_adjacency(a, b)
        self.assertEqual(adj.shared_length, F(2, 3))
        seg = adj.shared_segments[0]
        self.assertEqual(seg.start, (F(3, 2), F(1, 3)))
        self.assertEqual(seg.end, (F(3, 2), F(1)))

    def test_disjoint_polygons(self):
        a = [(0, 0), (1, 0), (1, 1), (0, 1)]
        b = [(5, 5), (6, 5), (6, 6), (5, 6)]
        adj = analyze_adjacency(a, b)
        self.assertFalse(adj.is_edge_adjacent)
        self.assertEqual(adj.shared_segments, ())
        self.assertEqual(adj.point_contacts, ())
        self.assertEqual(adj.shared_length, 0)

    def test_diagonal_shared_edge_exact_length(self):
        a = [(0, 0), (2, 0), (2, 2)]
        b = [(0, 0), (2, 2), (0, 2)]
        adj = analyze_adjacency(a, b)
        self.assertTrue(adj.is_edge_adjacent)
        seg = adj.shared_segments[0]
        self.assertEqual(seg.length_squared, F(8))
        # Irrational length falls back to float; square stays exact.
        self.assertAlmostEqual(seg.length, 2.8284271247461903)


class PointContactTests(unittest.TestCase):
    def test_corner_touch_is_not_edge_adjacency(self):
        a = [(0, 0), (1, 0), (1, 1), (0, 1)]
        b = [(1, 1), (2, 1), (2, 2), (1, 2)]
        adj = analyze_adjacency(a, b)
        self.assertFalse(adj.is_edge_adjacent)
        self.assertEqual(adj.shared_segments, ())
        self.assertEqual(adj.point_contacts, ((F(1), F(1)),))

    def test_t_junction_vertex_on_edge(self):
        # B's vertex touches the midpoint of A's right edge: point contact.
        a = [(0, 0), (2, 0), (2, 2), (0, 2)]
        b = [(2, 1), (3, 0), (3, 2)]
        adj = analyze_adjacency(a, b)
        self.assertFalse(adj.is_edge_adjacent)
        self.assertEqual(adj.point_contacts, ((F(2), F(1)),))

    def test_crossing_boundaries_give_point_contacts(self):
        a = [(0, 0), (4, 0), (4, 4), (0, 4)]
        b = [(2, -1), (3, -1), (3, 5), (2, 5)]
        adj = analyze_adjacency(a, b)
        self.assertFalse(adj.is_edge_adjacent)
        self.assertEqual(
            adj.point_contacts,
            ((F(2), F(0)), (F(2), F(4)), (F(3), F(0)), (F(3), F(4))),
        )

    def test_collinear_endpoint_touch_is_point_contact(self):
        # Edges are collinear but only meet at one endpoint.
        a = [(0, 0), (2, 0), (2, 1), (0, 1)]
        b = [(2, 0), (4, 0), (4, -1), (2, -1)]
        adj = analyze_adjacency(a, b)
        self.assertFalse(adj.is_edge_adjacent)
        self.assertEqual(adj.point_contacts, ((F(2), F(0)),))

    def test_shared_segment_endpoints_not_point_contacts(self):
        a = [(0, 0), (2, 0), (2, 2), (0, 2)]
        b = [(2, 0), (4, 0), (4, 1), (2, 1)]
        adj = analyze_adjacency(a, b)
        self.assertTrue(adj.is_edge_adjacent)
        self.assertEqual(adj.point_contacts, ())


class SnapTests(unittest.TestCase):
    def test_snap_reports_displacement_and_enables_adjacency(self):
        a = [(0, 0), (1, 0), (1, 1), (0, 1)]
        eps = F(1, 100)
        b = [(1 + eps, 0), (2, 0), (2, 1), (1 + eps, 1)]
        self.assertFalse(analyze_adjacency(a, b).is_edge_adjacent)

        snapped, events = snap_polygon_to_polygon(b, a, F(1, 10))
        moved = {e.index for e in events if e.moved}
        self.assertEqual(moved, {0, 3})
        for e in events:
            if e.index == 0:
                self.assertEqual(e.original, (F(101, 100), F(0)))
                self.assertEqual(e.result, (F(1), F(0)))
                self.assertEqual(e.displacement, (F(-1, 100), F(0)))
                self.assertEqual(e.distance_squared, F(1, 10000))
        adj = analyze_adjacency(a, snapped)
        self.assertTrue(adj.is_edge_adjacent)
        self.assertEqual(adj.shared_length, F(1))

    def test_snap_leaves_out_of_tolerance_vertices_untouched(self):
        # Only vertex 0 is near a target; vertices 1..3 must be identical.
        moving = [(F(1, 100), 0), (5, 0), (5, 5), (0, 5)]
        targets = [(0, 0), (9, 9)]
        snapped, events = snap_vertices(moving, targets, F(1, 10))
        self.assertEqual([e.index for e in events], [0])
        self.assertEqual(snapped[0], (F(0), F(0)))
        for i in (1, 2, 3):
            self.assertEqual(snapped[i], as_point(moving[i]))

    def test_snap_tolerance_boundary_is_exact(self):
        # Distance exactly equal to tolerance snaps; one ulp beyond does not.
        p = [(F(1, 3), 0)]
        target = [(0, 0)]
        snapped, events = snap_vertices(p, target, F(1, 3))
        self.assertEqual(snapped[0], (F(0), F(0)))
        self.assertEqual(len(events), 1)
        snapped2, events2 = snap_vertices(p, target, F(1, 3) - F(1, 10**9))
        self.assertEqual(snapped2[0], (F(1, 3), F(0)))
        self.assertEqual(events2, [])

    def test_snap_nearest_target_and_deterministic_tie(self):
        snapped, events = snap_vertices([(F(9, 10), 0)], [(0, 0), (2, 0)], F(2))
        self.assertEqual(snapped[0], (F(0), F(0)))
        # Equidistant tie -> lowest target index wins.
        snapped, _ = snap_vertices([(1, 0)], [(0, 0), (2, 0)], F(1))
        self.assertEqual(snapped[0], (F(0), F(0)))

    def test_snap_zero_tolerance_only_exact_matches(self):
        snapped, events = snap_vertices([(0, 0), (F(1, 2), 0)], [(0, 0)], 0)
        self.assertEqual(snapped[0], (F(0), F(0)))
        self.assertEqual(snapped[1], (F(1, 2), F(0)))

    def test_negative_tolerance_rejected(self):
        with self.assertRaises(ValueError):
            snap_vertices([(0, 0)], [(0, 0)], F(-1))


class DegenerateInputTests(unittest.TestCase):
    def test_too_few_vertices_rejected(self):
        with self.assertRaises(ValueError):
            analyze_adjacency([(0, 0), (1, 0)], [(0, 0), (1, 0), (1, 1)])

    def test_adjacency_type(self):
        adj = analyze_adjacency(
            [(0, 0), (1, 0), (1, 1), (0, 1)],
            [(1, 0), (2, 0), (2, 1), (1, 1)],
        )
        self.assertIsInstance(adj, Adjacency)


if __name__ == "__main__":
    unittest.main()
