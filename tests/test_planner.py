"""Unit tests for exact-clearance grid path planning.

Run with:  python3.11 -m unittest discover -s tests -v
"""

import os
import sys
import unittest
from fractions import Fraction

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from geometry import (as_point, dist2_point_shape, dist2_seg_shape,
                      point_in_polygon, segments_intersect)
from planner import GridPlanner

F = Fraction


class TestExactGeometry(unittest.TestCase):
    def test_segments_intersect_exact(self):
        self.assertTrue(segments_intersect(as_point((0, 0)), as_point((2, 2)),
                                           as_point((0, 2)), as_point((2, 0))))
        # Touching at an endpoint counts as intersecting (closed segments).
        self.assertTrue(segments_intersect(as_point((0, 0)), as_point((1, 0)),
                                           as_point((1, 0)), as_point((1, 1))))
        # Collinear overlap.
        self.assertTrue(segments_intersect(as_point((0, 0)), as_point((2, 0)),
                                           as_point((1, 0)), as_point((3, 0))))
        self.assertFalse(segments_intersect(as_point((0, 0)), as_point((1, 0)),
                                            as_point((0, 1)), as_point((1, 1))))

    def test_point_in_polygon_boundary_and_vertex_on_ray(self):
        sq = [as_point(p) for p in [(0, 0), (4, 0), (4, 4), (0, 4)]]
        self.assertTrue(point_in_polygon(as_point((2, 2)), sq))
        self.assertTrue(point_in_polygon(as_point((0, 2)), sq))   # on edge
        self.assertTrue(point_in_polygon(as_point((4, 4)), sq))   # on vertex
        self.assertTrue(point_in_polygon(as_point((2, 0)), sq))   # ray hits vertices
        self.assertFalse(point_in_polygon(as_point((5, 2)), sq))
        self.assertFalse(point_in_polygon(as_point((-1, 0)), sq))

    def test_distances_are_exact_rationals(self):
        seg = [as_point((0, 0)), as_point((4, 0))]
        d2 = dist2_point_shape(as_point((F(1, 3), F(1, 3))), seg)
        self.assertIsInstance(d2, Fraction)
        self.assertEqual(d2, F(1, 9))
        # Point exactly on the segment -> exactly zero, no epsilon.
        self.assertEqual(dist2_point_shape(as_point((2, 0)), seg), 0)


class TestClearanceConventions(unittest.TestCase):
    def test_touching_at_exactly_radius_is_allowed(self):
        # Point obstacle at origin, robot radius 1: the tangent segment
        # (-2,1)->(2,1) keeps distance exactly 1 and must be accepted.
        planner = GridPlanner([[(0, 0)]], radius=1, cell_size=1)
        self.assertTrue(planner.segment_clear((-2, 1), (2, 1)))
        # Distance 1/2 < 1 must be rejected.
        self.assertFalse(planner.segment_clear((-2, F(1, 2)), (2, F(1, 2))))

    def test_zero_radius_robot(self):
        box = [[(1, -1), (3, -1), (3, 1), (1, 1)]]
        planner = GridPlanner(box, radius=0, cell_size=1)
        # r=0: passing through the interior (dist 0) is blocked...
        self.assertFalse(planner.segment_clear((0, 0), (4, 0)))
        # ...and so is sliding exactly along the boundary (dist 0)...
        self.assertFalse(planner.segment_clear((0, 1), (4, 1)))
        # ...while any positive distance is fine.
        self.assertTrue(planner.segment_clear((0, 2), (4, 2)))
        res = planner.plan((0, 0), (4, 0))
        self.assertIsNotNone(res)
        self.assertTrue(res.ok)


class TestPlanning(unittest.TestCase):
    def test_open_field_straight_line(self):
        planner = GridPlanner([], radius=1, cell_size=1)
        res = planner.plan((0, 0), (5, 3))
        self.assertIsNotNone(res)
        self.assertEqual(res.path, [as_point((0, 0)), as_point((5, 3))])
        self.assertTrue(res.ok)
        self.assertTrue(all(s.min_dist2 is None for s in res.segments))

    def test_single_box_detour_and_clearance(self):
        box = [[(2, -1), (4, -1), (4, 1), (2, 1)]]
        planner = GridPlanner(box, radius=1, cell_size=1)
        res = planner.plan((0, 0), (6, 0))
        self.assertIsNotNone(res)
        self.assertGreater(len(res.path), 2)          # must detour
        self.assertTrue(res.ok)
        for s in res.segments:
            self.assertGreaterEqual(s.min_dist2, F(1))  # exact clearance r^2=1

    def test_simplification_is_revalidated(self):
        box = [[(2, -1), (4, -1), (4, 1), (2, 1)]]
        planner = GridPlanner(box, radius=1, cell_size=1)
        res = planner.plan((0, 0), (6, 0))
        self.assertLess(len(res.path), len(res.raw_path))  # simplification happened
        self.assertTrue(all(s.clear for s in res.segments))
        # A hand-made shortcut through the box must fail validation.
        checks = planner.validate_path([(0, 0), (6, 0)])
        self.assertFalse(checks[0].clear)
        self.assertEqual(checks[0].min_dist2, 0)

    def test_diagonal_gap_blocks_robot(self):
        # Two diagonal walls whose corner gap is sqrt(2) < 2r = 2:
        # the inflated walls overlap and seal the passage.
        walls = [
            [(-12, 1), (0, 1), (0, 3), (-12, 3)],
            [(1, -2), (12, -2), (12, 0), (1, 0)],
        ]
        planner = GridPlanner(walls, radius=1, cell_size=1,
                              bounds=(-10, -6, 10, 6))
        self.assertIsNone(planner.plan((-5, 4), (5, -4)))

    def test_diagonal_gap_passable_when_wide_enough(self):
        # Same layout but the corner gap is sqrt(10) > 2r = 2.
        walls = [
            [(-12, 1), (0, 1), (0, 3), (-12, 3)],
            [(3, -2), (12, -2), (12, 0), (3, 0)],
        ]
        planner = GridPlanner(walls, radius=1, cell_size=1,
                              bounds=(-10, -6, 10, 6))
        res = planner.plan((-5, 4), (5, -4))
        self.assertIsNotNone(res)
        self.assertTrue(res.ok)
        for s in res.segments:
            self.assertGreaterEqual(s.min_dist2, F(1))

    def test_start_covered_by_inflated_obstacle(self):
        box = [[(0, 0), (6, 0), (6, 6), (0, 6)]]
        planner = GridPlanner(box, radius=1, cell_size=1)
        # Start inside the obstacle.
        self.assertIsNone(planner.plan((3, 3), (10, 3)))
        # Start outside the polygon but within radius of its edge.
        self.assertIsNone(planner.plan((F(13, 2), 3), (10, 3)))
        # Start at distance exactly r: allowed, straight clear path exists.
        res = planner.plan((7, 3), (10, 3))
        self.assertIsNotNone(res)
        self.assertEqual(res.path, [as_point((7, 3)), as_point((10, 3))])

    def test_goal_covered_by_inflated_obstacle(self):
        box = [[(0, 0), (6, 0), (6, 6), (0, 6)]]
        planner = GridPlanner(box, radius=1, cell_size=1)
        self.assertIsNone(planner.plan((10, 3), (3, 3)))
        self.assertIsNone(planner.plan((10, 3), (F(13, 2), 3)))

    def test_no_corner_cutting_rule(self):
        planner = GridPlanner([], radius=0, cell_size=1, bounds=(0, 0, 4, 4))
        planner._build_grid(as_point((0, 0)), as_point((3, 3)))
        planner._forced_free = set()
        planner.blocked[2][1] = True  # single blocked cell
        neigh = dict(planner._neighbors((2, 0)))
        # Both diagonals from (2,0) require the blocked orthogonal (2,1).
        self.assertNotIn((3, 1), neigh)
        self.assertNotIn((1, 1), neigh)
        self.assertIn((3, 0), neigh)      # orthogonal moves unaffected
        self.assertIn((1, 0), neigh)
        # From (1,1): diagonals touching the blocked cell's row are cut...
        neigh = dict(planner._neighbors((2, 2)))
        self.assertNotIn((3, 1), neigh)
        self.assertNotIn((1, 1), neigh)
        self.assertIn((3, 3), neigh)
        # ...while a diagonal with both orthogonals free is allowed.
        neigh = dict(planner._neighbors((0, 0)))
        self.assertIn((1, 1), neigh)

    def test_degenerate_point_obstacle(self):
        planner = GridPlanner([[(3, 0)]], radius=1, cell_size=1)
        res = planner.plan((0, 0), (6, 0))
        self.assertIsNotNone(res)
        self.assertTrue(res.ok)
        for s in res.segments:
            self.assertGreaterEqual(s.min_dist2, F(1))

    def test_degenerate_segment_obstacle(self):
        wall = [[(2, -3), (2, 3)]]  # zero-width wall; dilation is a capsule
        planner = GridPlanner(wall, radius=F(1, 2), cell_size=1)
        res = planner.plan((0, 0), (4, 0))
        self.assertIsNotNone(res)
        self.assertTrue(res.ok)
        for s in res.segments:
            self.assertGreaterEqual(s.min_dist2, F(1, 4))

    def test_rational_coordinates_stay_exact(self):
        obs = [[(F(1, 3), F(1, 3)), (F(2, 3), F(1, 3)),
                (F(2, 3), F(2, 3)), (F(1, 3), F(2, 3))]]
        planner = GridPlanner(obs, radius=F(1, 4), cell_size=F(1, 3))
        res = planner.plan((0, 0), (2, 1))
        self.assertIsNotNone(res)
        self.assertTrue(res.ok)
        for p in res.path:
            self.assertIsInstance(p[0], Fraction)
            self.assertIsInstance(p[1], Fraction)
        for s in res.segments:
            self.assertIsInstance(s.min_dist2, Fraction)
            self.assertGreaterEqual(s.min_dist2, F(1, 16))

    def test_walled_arena_no_path(self):
        # A wall spanning the whole arena width separates start and goal.
        wall = [[(0, 4), (10, 4), (10, 6), (0, 6)]]
        planner = GridPlanner(wall, radius=1, cell_size=1, bounds=(0, 0, 10, 10))
        self.assertIsNone(planner.plan((5, 1), (5, 9)))

    def test_bounds_respected(self):
        planner = GridPlanner([], radius=1, cell_size=1, bounds=(0, 0, 10, 10))
        self.assertIsNone(planner.plan((-1, 5), (5, 5)))   # start outside
        res = planner.plan((1, 1), (9, 9))
        self.assertIsNotNone(res)
        self.assertTrue(res.ok)

    def test_validate_path_reports_per_segment(self):
        box = [[(2, -1), (4, -1), (4, 1), (2, 1)]]
        planner = GridPlanner(box, radius=1, cell_size=1)
        checks = planner.validate_path([(0, 3), (6, 3), (3, 0)])
        self.assertEqual(len(checks), 2)
        self.assertTrue(checks[0].clear)    # passes above the box
        self.assertFalse(checks[1].clear)   # dives into the box
        self.assertEqual(checks[1].min_dist2, 0)


if __name__ == "__main__":
    unittest.main()
