"""Unittest suite for pointlocation (exact multi-ring point location)."""

import random
import unittest
from fractions import Fraction as F

from pointlocation import Location, Locator, normalize_ring

INSIDE, OUTSIDE, BOUNDARY = Location.INSIDE, Location.OUTSIDE, Location.BOUNDARY

SQUARE = [(0, 0), (10, 0), (10, 10), (0, 10)]
HOLE = [(2, 2), (4, 2), (4, 4), (2, 4)]
# Concave pentagon: square [0,4]^2 with a V-notch from the top down to (2,2).
NOTCHED = [(0, 0), (4, 0), (4, 4), (2, 2), (0, 4)]


def brute_force(outer, holes, p):
    """Index-free reference implementation used to cross-check the Locator."""
    p = (F(p[0]), F(p[1]))

    def on_segment(a, b):
        cross = (b[0] - a[0]) * (p[1] - a[1]) - (b[1] - a[1]) * (p[0] - a[0])
        if cross != 0:
            return False
        return (
            min(a[0], b[0]) <= p[0] <= max(a[0], b[0])
            and min(a[1], b[1]) <= p[1] <= max(a[1], b[1])
        )

    def inside(ring):
        if len(ring) < 3:
            return False
        count = 0
        for i in range(len(ring)):
            a, b = ring[i], ring[(i + 1) % len(ring)]
            if (a[1] > p[1]) != (b[1] > p[1]):
                x = a[0] + (p[1] - a[1]) * (b[0] - a[0]) / (b[1] - a[1])
                if x > p[0]:
                    count += 1
        return count % 2 == 1

    rings = [outer, *holes]
    for ring in rings:
        if len(ring) >= 2:
            for i in range(len(ring)):
                if on_segment(ring[i], ring[(i + 1) % len(ring)]):
                    return BOUNDARY
    if not inside(outer):
        return OUTSIDE
    if any(inside(h) for h in holes):
        return OUTSIDE
    return INSIDE


class TestBasics(unittest.TestCase):
    def setUp(self):
        self.loc = Locator(SQUARE)

    def test_inside(self):
        self.assertIs(self.loc.locate((5, 5)), INSIDE)
        self.assertIs(self.loc.locate((1, 9)), INSIDE)

    def test_outside(self):
        self.assertIs(self.loc.locate((-1, 5)), OUTSIDE)
        self.assertIs(self.loc.locate((11, 5)), OUTSIDE)
        self.assertIs(self.loc.locate((5, -1)), OUTSIDE)
        self.assertIs(self.loc.locate((100, 100)), OUTSIDE)

    def test_boundary_edges_and_vertices(self):
        for p in [(0, 5), (10, 5), (5, 0), (5, 10),
                  (0, 0), (10, 0), (10, 10), (0, 10)]:
            self.assertIs(self.loc.locate(p), BOUNDARY, p)

    def test_orientation_is_irrelevant(self):
        ccw = Locator(SQUARE)
        cw = Locator(list(reversed(SQUARE)))
        for p in [(5, 5), (0, 5), (50, 50)]:
            self.assertIs(ccw.locate(p), cw.locate(p))


class TestHoles(unittest.TestCase):
    def setUp(self):
        self.loc = Locator(SQUARE, [HOLE])

    def test_hole_interior_is_outside(self):
        self.assertIs(self.loc.locate((3, 3)), OUTSIDE)

    def test_between_outer_and_hole_is_inside(self):
        self.assertIs(self.loc.locate((1, 1)), INSIDE)
        self.assertIs(self.loc.locate((3, 5)), INSIDE)

    def test_hole_edge_is_boundary(self):
        self.assertIs(self.loc.locate((3, 2)), BOUNDARY)
        self.assertIs(self.loc.locate((2, 3)), BOUNDARY)

    def test_hole_vertex_is_boundary(self):
        self.assertIs(self.loc.locate((2, 2)), BOUNDARY)
        self.assertIs(self.loc.locate((4, 4)), BOUNDARY)

    def test_multiple_holes(self):
        loc = Locator(SQUARE, [HOLE, [(6, 6), (8, 6), (8, 8), (6, 8)]])
        self.assertIs(loc.locate((3, 3)), OUTSIDE)
        self.assertIs(loc.locate((7, 7)), OUTSIDE)
        self.assertIs(loc.locate((5, 5)), INSIDE)

    def test_hole_outside_outer_follows_boolean_formula(self):
        loc = Locator(SQUARE, [[(20, 20), (30, 20), (30, 30), (20, 30)]])
        self.assertIs(loc.locate((25, 25)), OUTSIDE)  # in hole, not in outer
        self.assertIs(loc.locate((5, 5)), INSIDE)


class TestRayThroughVertex(unittest.TestCase):
    def setUp(self):
        self.loc = Locator(NOTCHED)

    def test_ray_through_local_extremum_not_double_counted(self):
        # Ray y=2 passes exactly through the notch tip vertex (2,2).
        self.assertIs(self.loc.locate((-1, 2)), OUTSIDE)
        self.assertIs(self.loc.locate((1, 2)), INSIDE)
        self.assertIs(self.loc.locate((3, 2)), INSIDE)
        self.assertIs(self.loc.locate((5, 2)), OUTSIDE)

    def test_notch_interior_is_outside(self):
        self.assertIs(self.loc.locate((2, 3)), OUTSIDE)
        self.assertIs(self.loc.locate((2, 1)), INSIDE)

    def test_ray_through_convex_vertex(self):
        tri = Locator([(0, 0), (6, 0), (3, 6)])
        # Ray y=6 passes exactly through the apex (3,6): zero crossings.
        self.assertIs(tri.locate((1, 6)), OUTSIDE)
        self.assertIs(tri.locate((4, 6)), OUTSIDE)
        # Ray y=3 crosses both slanted edges once each.
        self.assertIs(tri.locate((1, 3)), OUTSIDE)
        self.assertIs(tri.locate((2, 3)), INSIDE)
        self.assertIs(tri.locate((5, 3)), OUTSIDE)

    def test_horizontal_edge_not_counted_as_crossing(self):
        # Points just above/below the horizontal top edge of a square.
        loc = Locator(SQUARE)
        self.assertIs(loc.locate((5, 9)), INSIDE)
        self.assertIs(loc.locate((5, 11)), OUTSIDE)


class TestDegenerateInputs(unittest.TestCase):
    def test_repeated_closing_point_changes_nothing(self):
        closed = Locator([(0, 0), (10, 0), (10, 10), (0, 10), (0, 0)])
        plain = Locator(SQUARE)
        for p in [(5, 5), (0, 5), (0, 0), (20, 20), (F(1, 2), F(1, 2))]:
            self.assertIs(closed.locate(p), plain.locate(p), p)

    def test_consecutive_duplicates_removed(self):
        ring = [(0, 0), (0, 0), (10, 0), (10, 10), (10, 10), (0, 10)]
        self.assertEqual(normalize_ring(ring), normalize_ring(SQUARE))
        loc = Locator(ring)
        self.assertIs(loc.locate((5, 5)), INSIDE)
        self.assertIs(loc.locate((10, 5)), BOUNDARY)

    def test_ring_with_two_points_has_no_interior(self):
        loc = Locator([(0, 0), (4, 4)])
        self.assertIs(loc.locate((2, 2)), BOUNDARY)   # on the segment
        self.assertIs(loc.locate((0, 1)), OUTSIDE)    # off the segment
        self.assertIs(loc.locate((5, 5)), OUTSIDE)    # past the endpoint

    def test_ring_with_one_point(self):
        loc = Locator([(3, 3)])
        self.assertIs(loc.locate((3, 3)), OUTSIDE)  # isolated point, no edge
        self.assertIs(loc.locate((0, 0)), OUTSIDE)

    def test_empty_ring(self):
        loc = Locator([])
        self.assertIs(loc.locate((0, 0)), OUTSIDE)

    def test_zero_area_collinear_ring(self):
        loc = Locator([(0, 0), (2, 0), (4, 0)])
        self.assertIs(loc.locate((1, 0)), BOUNDARY)
        self.assertIs(loc.locate((2, 1)), OUTSIDE)

    def test_degenerate_hole_still_marks_boundary(self):
        loc = Locator(SQUARE, [[(5, 1), (5, 9)]])
        self.assertIs(loc.locate((5, 5)), BOUNDARY)
        self.assertIs(loc.locate((4, 5)), INSIDE)


class TestExactArithmetic(unittest.TestCase):
    def test_fraction_coordinates(self):
        loc = Locator([(0, 0), (F(9, 2), 0), (F(9, 2), F(9, 2)), (0, F(9, 2))])
        self.assertIs(loc.locate((F(9, 4), F(9, 4))), INSIDE)
        self.assertIs(loc.locate((F(9, 2), F(9, 4))), BOUNDARY)
        self.assertIs(loc.locate((F(10, 2), 1)), OUTSIDE)

    def test_thirds_are_exact(self):
        # 1/3 and 2/3 arithmetic must be exact, no epsilon involved.
        loc = Locator([(0, 0), (1, 0), (1, 1), (0, 1)])
        self.assertIs(loc.locate((F(1, 3), F(2, 3))), INSIDE)
        self.assertIs(loc.locate((F(1, 3), 1)), BOUNDARY)
        self.assertIs(loc.locate((F(1, 3), F(4, 3))), OUTSIDE)

    def test_rational_point_on_slanted_edge(self):
        loc = Locator([(0, 0), (6, 0), (0, 6)])
        # x + y = 6 on the hypotenuse; 7/2 + 5/2 = 6 exactly.
        self.assertIs(loc.locate((F(7, 2), F(5, 2))), BOUNDARY)
        self.assertIs(loc.locate((F(7, 2), F(5, 2) - F(1, 10**12))), INSIDE)
        self.assertIs(loc.locate((F(7, 2), F(5, 2) + F(1, 3))), OUTSIDE)

    def test_float_rejected(self):
        with self.assertRaises(TypeError):
            Locator([(0.0, 0), (1, 0), (0, 1)])
        loc = Locator(SQUARE)
        with self.assertRaises(TypeError):
            loc.locate((0.5, 0.5))

    def test_bool_rejected(self):
        with self.assertRaises(TypeError):
            Locator([(True, 0), (1, 0), (0, 1)])


class TestBatchAndIndex(unittest.TestCase):
    def test_batch_matches_single(self):
        loc = Locator(SQUARE, [HOLE])
        pts = [(i, j) for i in range(-2, 13) for j in range(-2, 13)]
        self.assertEqual(loc.locate_many(pts), [loc.locate(p) for p in pts])

    def test_matches_brute_force_on_random_points(self):
        outer = [(0, 0), (9, 1), (12, 7), (6, 5), (7, 12), (1, 9)]
        holes = [[(2, 2), (5, 2), (5, 4), (2, 4)],
                 [(8, 8), (10, 8), (9, 10)]]
        loc = Locator(outer, holes)
        rng = random.Random(20260924)
        pts = [
            (F(rng.randrange(-4, 33), 2), F(rng.randrange(-4, 33), 2))
            for _ in range(2000)
        ]
        expected = [brute_force(outer, holes, p) for p in pts]
        self.assertEqual(loc.locate_many(pts), expected)

    def test_grid_boundary_lookup_finds_all_boundary_points(self):
        loc = Locator(SQUARE, [HOLE])
        boundary = [(i, 0) for i in range(11)] + [(i, 10) for i in range(11)]
        boundary += [(0, i) for i in range(11)] + [(10, i) for i in range(11)]
        boundary += [(i, 2) for i in range(2, 5)] + [(i, 4) for i in range(2, 5)]
        boundary += [(2, i) for i in range(2, 5)] + [(4, i) for i in range(2, 5)]
        for p in boundary:
            self.assertIs(loc.locate(p), BOUNDARY, p)


if __name__ == "__main__":
    unittest.main(verbosity=2)
