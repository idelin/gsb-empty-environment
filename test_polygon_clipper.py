"""Unit tests for polygon_clipper (exact convex-window clipping)."""

import unittest
from fractions import Fraction

from polygon_clipper import (
    DegenerateInputError,
    NonConvexWindowError,
    PolygonClipperError,
    SelfIntersectionError,
    clip_polygon,
    polygon_area,
)

F = Fraction

BOX = [(0, 0), (10, 0), (10, 10), (0, 10)]  # ccw unit test window

# U-shaped simple polygon (opening upwards), area 10.
U_SHAPE = [(0, 0), (4, 0), (4, 4), (3, 4), (3, 1), (1, 1), (1, 4), (0, 4)]
# Window slicing off the bottom bar of the U: keeps y >= 2.
U_WINDOW = [(-1, 2), (5, 2), (5, 5), (-1, 5)]

# Trident: three bars joined at the bottom, area 14.
TRIDENT = [(0, 0), (5, 0), (5, 4), (4, 4), (4, 1), (3, 1),
           (3, 4), (2, 4), (2, 1), (1, 1), (1, 4), (0, 4)]
TRIDENT_WINDOW = [(-1, 2), (6, 2), (6, 6), (-1, 6)]


def vset(region):
    return set(region.vertices)


class TestInputValidation(unittest.TestCase):
    def test_bowtie_rejected(self):
        bowtie = [(0, 0), (2, 2), (2, 0), (0, 2)]
        with self.assertRaises(SelfIntersectionError):
            clip_polygon(bowtie, BOX)

    def test_repeated_vertex_rejected(self):
        with self.assertRaises(SelfIntersectionError):
            clip_polygon([(0, 0), (2, 0), (2, 2), (0, 0)], BOX)

    def test_vertex_on_nonadjacent_edge_rejected(self):
        # vertex (2, 0) lies on edge (0,0)-(4,0)
        with self.assertRaises(SelfIntersectionError):
            clip_polygon([(0, 0), (4, 0), (4, 2), (2, 2), (2, 0)], BOX)

    def test_backtracking_edge_rejected(self):
        # edge (2,0)->(1,0) folds back onto (0,0)->(2,0)
        with self.assertRaises(SelfIntersectionError):
            clip_polygon([(0, 0), (2, 0), (1, 0), (1, 1)], BOX)

    def test_zero_area_rejected(self):
        with self.assertRaises(PolygonClipperError):
            clip_polygon([(0, 0), (1, 0), (2, 0)], BOX)

    def test_too_few_vertices_rejected(self):
        with self.assertRaises(DegenerateInputError):
            clip_polygon([(0, 0), (1, 1)], BOX)

    def test_nonconvex_window_rejected(self):
        with self.assertRaises(NonConvexWindowError):
            clip_polygon([(0, 0), (1, 0), (0, 1)],
                         [(0, 0), (4, 0), (4, 4), (2, 1), (0, 4)])

    def test_self_intersecting_window_rejected(self):
        with self.assertRaises(SelfIntersectionError):
            clip_polygon([(0, 0), (1, 0), (0, 1)],
                         [(0, 0), (2, 2), (2, 0), (0, 2)])

    def test_float_coordinate_rejected(self):
        with self.assertRaises(TypeError):
            clip_polygon([(0.5, 0), (1, 0), (0, 1)], BOX)

    def test_rational_string_coordinate_accepted(self):
        result = clip_polygon([(0, 0), ("2", 0), (0, "1/3")], BOX)
        self.assertEqual(result.total_area, F(1, 3))

    def test_straight_continuation_vertex_allowed(self):
        # collinear consecutive edges that do not fold back are simple
        result = clip_polygon([(0, 0), (1, 0), (2, 0), (2, 2), (0, 2)], BOX)
        self.assertEqual(result.total_area, 4)


class TestBasicClipping(unittest.TestCase):
    def test_subject_inside_window_unchanged(self):
        tri = [(1, 1), (4, 1), (1, 4)]
        result = clip_polygon(tri, BOX)
        self.assertEqual(len(result.regions), 1)
        self.assertEqual(vset(result.regions[0]),
                         {(F(1), F(1)), (F(4), F(1)), (F(1), F(4))})
        self.assertEqual(result.total_area, F(9, 2))

    def test_window_inside_subject(self):
        window = [(2, 2), (4, 2), (4, 4), (2, 4)]
        result = clip_polygon(BOX, window)
        self.assertEqual(len(result.polygons), 1)
        self.assertEqual(result.total_area, 4)
        self.assertEqual(vset(result.regions[0]),
                         {(F(2), F(2)), (F(4), F(2)), (F(4), F(4)), (F(2), F(4))})

    def test_identical_polygons_boundary_kept(self):
        square = [(0, 0), (4, 0), (4, 4), (0, 4)]
        result = clip_polygon(square, square)
        self.assertEqual(len(result.polygons), 1)
        self.assertEqual(result.total_area, 16)

    def test_shared_edge_boundary_kept(self):
        # window edge 0 coincides with subject edge 0
        subject = [(0, 0), (4, 0), (4, 4), (0, 4)]
        window = [(0, 0), (10, 0), (10, 10), (0, 10)]
        result = clip_polygon(subject, window)
        self.assertEqual(result.total_area, 16)
        edge = next(e for e in result.regions[0].edges
                    if {e.a, e.b} == {(F(0), F(0)), (F(4), F(0))})
        self.assertEqual(edge.subject_edges, (0,))
        self.assertEqual(edge.window_edges, (0,))

    def test_vertex_exactly_on_window_edge_kept(self):
        # apex (2, 0) lies exactly on the window boundary y = 0
        tri = [(0, 1), (4, 1), (2, 0)]
        result = clip_polygon(tri, BOX)
        self.assertEqual(result.total_area, polygon_area(
            [(F(0), F(1)), (F(4), F(1)), (F(2), F(0))]))
        self.assertIn((F(2), F(0)), vset(result.regions[0]))

    def test_empty_when_disjoint(self):
        far = [(20, 20), (22, 20), (20, 22)]
        result = clip_polygon(far, BOX)
        self.assertEqual(result.regions, ())
        self.assertEqual(result.total_area, 0)

    def test_triangle_cut_exact_rationals(self):
        tri = [(0, 0), (2, 0), (0, 2)]
        window = [(F(1, 2), -1), (3, -1), (3, 3), (F(1, 2), 3)]
        result = clip_polygon(tri, window)
        self.assertEqual(len(result.polygons), 1)
        self.assertEqual(vset(result.regions[0]),
                         {(F(1, 2), F(0)), (F(2), F(0)), (F(1, 2), F(3, 2))})
        self.assertEqual(result.total_area, F(9, 8))
        self.assertLessEqual(result.total_area, polygon_area(
            [(F(0), F(0)), (F(2), F(0)), (F(0), F(2))]))

    def test_clockwise_window_accepted(self):
        window = [(-1, 5), (5, 5), (5, 2), (-1, 2)]  # cw version of U_WINDOW
        result = clip_polygon(U_SHAPE, window)
        self.assertEqual(len(result.polygons), 2)
        self.assertEqual(result.total_area, 4)

    def test_clockwise_subject_accepted(self):
        result = clip_polygon(list(reversed(U_SHAPE)), U_WINDOW)
        self.assertEqual(len(result.polygons), 2)
        self.assertEqual(result.total_area, 4)


class TestMultipleRegions(unittest.TestCase):
    def test_u_shape_splits_into_two_regions(self):
        result = clip_polygon(U_SHAPE, U_WINDOW)
        self.assertEqual(len(result.polygons), 2)
        expected = [
            {(F(0), F(2)), (F(1), F(2)), (F(1), F(4)), (F(0), F(4))},
            {(F(3), F(2)), (F(4), F(2)), (F(4), F(4)), (F(3), F(4))},
        ]
        self.assertEqual(sorted(map(sorted, map(list, map(vset, result.polygons)))),
                         sorted(map(sorted, map(list, expected))))
        for region in result.polygons:
            self.assertEqual(region.area, 2)
        self.assertEqual(result.total_area, 4)
        self.assertLessEqual(result.total_area, 10)

    def test_trident_splits_into_three_regions(self):
        result = clip_polygon(TRIDENT, TRIDENT_WINDOW)
        self.assertEqual(len(result.polygons), 3)
        self.assertEqual(result.total_area, 6)
        for region in result.polygons:
            self.assertEqual(region.area, 2)

    def test_region_edge_provenance(self):
        result = clip_polygon(U_SHAPE, U_WINDOW)
        left = next(r for r in result.polygons
                    if (F(0), F(2)) in r.vertices)
        by_edge = {frozenset((e.a, e.b)): e for e in left.edges}
        bottom = by_edge[frozenset({(F(0), F(2)), (F(1), F(2))})]
        self.assertEqual(bottom.subject_edges, ())
        self.assertEqual(bottom.window_edges, (0,))
        top = by_edge[frozenset({(F(0), F(4)), (F(1), F(4))})]
        self.assertEqual(top.subject_edges, (6,))
        self.assertEqual(top.window_edges, ())


class TestDegenerateResults(unittest.TestCase):
    def test_point_result(self):
        # triangle touches the window only at the corner (0, 0)
        tri = [(-2, -1), (-1, -2), (0, 0)]
        window = [(0, 0), (4, 0), (4, 4), (0, 4)]
        result = clip_polygon(tri, window)
        self.assertEqual(len(result.regions), 1)
        region = result.regions[0]
        self.assertEqual(region.kind, "point")
        self.assertTrue(region.degenerate)
        self.assertEqual(region.area, 0)
        self.assertEqual(region.vertices, ((F(0), F(0)),))
        self.assertTrue(region.on_subject_edges)
        self.assertTrue(region.on_window_edges)

    def test_line_result(self):
        # only the edge (0,0)-(2,0) of the triangle lies in the window
        tri = [(0, 0), (2, 0), (0, -2)]
        window = [(0, 0), (4, 0), (4, 4), (0, 4)]
        result = clip_polygon(tri, window)
        self.assertEqual(len(result.regions), 1)
        region = result.regions[0]
        self.assertEqual(region.kind, "line")
        self.assertTrue(region.degenerate)
        self.assertEqual(region.area, 0)
        self.assertEqual(vset(region), {(F(0), F(0)), (F(2), F(0))})
        edge = region.edges[0]
        self.assertEqual(edge.subject_edges, (0,))
        self.assertEqual(edge.window_edges, (0,))

    def test_degenerate_property(self):
        result = clip_polygon([(-2, -1), (-1, -2), (0, 0)],
                              [(0, 0), (4, 0), (4, 4), (0, 4)])
        self.assertEqual(len(result.degenerate), 1)
        self.assertEqual(result.polygons, ())


class TestProvenance(unittest.TestCase):
    def test_cut_edge_has_window_source(self):
        tri = [(0, 0), (4, 0), (0, 4)]
        window = [(2, -1), (5, -1), (5, 5), (2, 5)]  # keeps x >= 2
        result = clip_polygon(tri, window)
        self.assertEqual(len(result.polygons), 1)
        region = result.polygons[0]
        self.assertEqual(vset(region),
                         {(F(2), F(0)), (F(4), F(0)), (F(2), F(2))})
        by_edge = {frozenset((e.a, e.b)): e for e in region.edges}
        cut = by_edge[frozenset({(F(2), F(0)), (F(2), F(2))})]
        self.assertEqual(cut.subject_edges, ())
        self.assertEqual(cut.window_edges, (3,))
        kept0 = by_edge[frozenset({(F(2), F(0)), (F(4), F(0))})]
        self.assertEqual(kept0.subject_edges, (0,))
        self.assertEqual(kept0.window_edges, ())
        kept1 = by_edge[frozenset({(F(4), F(0)), (F(2), F(2))})]
        self.assertEqual(kept1.subject_edges, (1,))

    def test_window_inside_subject_sources(self):
        window = [(2, 2), (4, 2), (4, 4), (2, 4)]
        result = clip_polygon(BOX, window)
        for edge in result.regions[0].edges:
            self.assertEqual(edge.subject_edges, ())
            self.assertEqual(len(edge.window_edges), 1)


class TestInvariants(unittest.TestCase):
    CASES = [
        ("u_shape", U_SHAPE, U_WINDOW),
        ("trident", TRIDENT, TRIDENT_WINDOW),
        ("tri_cut", [(0, 0), (2, 0), (0, 2)],
         [(F(1, 2), -1), (3, -1), (3, 3), (F(1, 2), 3)]),
        ("tri_window", [(0, 0), (4, 0), (0, 4)], [(2, -1), (5, -1), (5, 5), (2, 5)]),
        ("inside", [(1, 1), (4, 1), (1, 4)], BOX),
    ]

    def test_area_never_exceeds_subject(self):
        for name, subject, window in self.CASES:
            with self.subTest(case=name):
                result = clip_polygon(subject, window)
                subject_area = polygon_area(
                    [(F(x), F(y)) for x, y in subject])
                self.assertLessEqual(result.total_area, subject_area)

    def test_idempotent(self):
        for name, subject, window in self.CASES:
            with self.subTest(case=name):
                first = clip_polygon(subject, window)
                again_regions = []
                for region in first.polygons:
                    reclipped = clip_polygon(list(region.vertices), window)
                    self.assertEqual(reclipped.degenerate, ())
                    again_regions.extend(reclipped.polygons)
                self.assertEqual(len(again_regions), len(first.polygons))
                self.assertEqual(
                    sorted(r.area for r in again_regions),
                    sorted(r.area for r in first.polygons))
                self.assertEqual(
                    sorted(tuple(sorted(vset(r))) for r in again_regions),
                    sorted(tuple(sorted(vset(r))) for r in first.polygons))
                second_area = sum((r.area for r in again_regions), F(0))
                self.assertEqual(second_area, first.total_area)


if __name__ == "__main__":
    unittest.main()
