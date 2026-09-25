import unittest
from fractions import Fraction

from geom import (
    on_segment,
    orient,
    point_in_polygon,
    point_polygon_dist2,
    point_seg_dist2,
    seg_polygon_dist2,
    segment_enters_interior,
    segments_intersect,
    to_point,
    validate_polygon,
)
from planner import Bounds, GridPlanner, NoPathError
from validate import path_valid, validate_path

F = Fraction


def P(x, y):
    return (F(x), F(y))


class TestExactGeometry(unittest.TestCase):
    def test_orient(self):
        self.assertEqual(orient(P(0, 0), P(1, 0), P(0, 1)), 1)
        self.assertEqual(orient(P(0, 0), P(0, 1), P(1, 0)), -1)
        self.assertEqual(orient(P(0, 0), P(1, 1), P(2, 2)), 0)

    def test_segments_intersect(self):
        self.assertTrue(segments_intersect(P(0, 0), P(2, 2), P(0, 2), P(2, 0)))
        self.assertTrue(segments_intersect(P(0, 0), P(1, 0), P(1, 0), P(1, 1)))  # touch
        self.assertTrue(segments_intersect(P(0, 0), P(2, 0), P(1, 0), P(3, 0)))  # overlap
        self.assertFalse(segments_intersect(P(0, 0), P(1, 0), P(0, 1), P(1, 1)))

    def test_point_seg_dist2_exact(self):
        # distance from origin to segment (1,0)-(0,1) is sqrt(2)/2 -> squared 1/2
        self.assertEqual(point_seg_dist2(P(0, 0), P(1, 0), P(0, 1)), F(1, 2))
        self.assertEqual(point_seg_dist2(P(5, 0), P(1, 0), P(0, 1)), F(16))

    def test_point_in_polygon(self):
        sq = [P(0, 0), P(4, 0), P(4, 4), P(0, 4)]
        self.assertTrue(point_in_polygon(P(2, 2), sq))
        self.assertFalse(point_in_polygon(P(5, 2), sq))
        self.assertFalse(point_in_polygon(P(0, 2), sq))   # boundary is not interior
        self.assertFalse(point_in_polygon(P(4, 4), sq))   # vertex is not interior
        # ray passes exactly through a vertex of an L-shaped polygon
        lshape = [P(0, 0), P(4, 0), P(4, 2), P(2, 2), P(2, 4), P(0, 4)]
        self.assertTrue(point_in_polygon(P(1, 3), lshape))
        self.assertFalse(point_in_polygon(P(3, 3), lshape))

    def test_segment_enters_interior(self):
        sq = [P(0, 0), P(4, 0), P(4, 4), P(0, 4)]
        self.assertTrue(segment_enters_interior(P(-1, 2), P(5, 2), sq))
        self.assertTrue(segment_enters_interior(P(1, 1), P(2, 2), sq))
        # through two vertices diagonally: (-1,-1) -> (5,5) crosses interior
        self.assertTrue(segment_enters_interior(P(-1, -1), P(5, 5), sq))
        # touching an edge without entering is allowed
        self.assertFalse(segment_enters_interior(P(-1, 0), P(5, 0), sq))
        self.assertFalse(segment_enters_interior(P(-1, -1), P(-1, 5), sq))

    def test_seg_polygon_dist2(self):
        sq = [P(0, 0), P(2, 0), P(2, 2), P(0, 2)]
        self.assertEqual(seg_polygon_dist2(P(4, 0), P(4, 2), sq), F(4))
        self.assertEqual(seg_polygon_dist2(P(1, 1), P(5, 5), sq), F(0))
        self.assertEqual(seg_polygon_dist2(P(0, 0), P(9, 9), sq), F(0))


class TestDegenerateInputs(unittest.TestCase):
    def setUp(self):
        self.bounds = Bounds.of(0, 0, 10, 10)

    def test_too_few_vertices(self):
        with self.assertRaises(ValueError):
            GridPlanner([[(1, 1), (2, 2)]], 0, self.bounds)

    def test_duplicate_vertex(self):
        with self.assertRaises(ValueError):
            GridPlanner([[(1, 1), (1, 1), (3, 1), (2, 3)]], 0, self.bounds)

    def test_collinear_zero_area(self):
        with self.assertRaises(ValueError):
            GridPlanner([[(0, 0), (1, 1), (2, 2)]], 0, self.bounds)

    def test_self_intersecting_bowtie(self):
        with self.assertRaises(ValueError):
            GridPlanner(
                [[(0, 0), (2, 2), (2, 0), (0, 2)]], 0, self.bounds
            )

    def test_negative_radius(self):
        with self.assertRaises(ValueError):
            GridPlanner([], -1, self.bounds)

    def test_float_coordinates_rejected(self):
        with self.assertRaises(TypeError):
            GridPlanner([], 0, self.bounds).plan((0.5, 1), (2, 2))


class TestPlanning(unittest.TestCase):
    def test_empty_room_straight_line(self):
        bounds = Bounds.of(0, 0, 10, 10)
        planner = GridPlanner([], "1/2", bounds, step=1)
        plan = planner.plan((1, 1), (9, 9))
        self.assertEqual(plan.waypoints, [P(1, 1), P(9, 9)])
        self.assertTrue(path_valid(plan.waypoints, [], "1/2", bounds))

    def test_start_equals_goal(self):
        bounds = Bounds.of(0, 0, 10, 10)
        planner = GridPlanner([], "1/2", bounds)
        plan = planner.plan((3, 3), (3, 3))
        self.assertEqual(plan.waypoints, [P(3, 3)])

    def test_box_detour_with_clearance(self):
        bounds = Bounds.of(0, 0, 10, 10)
        box = [[(4, 4), (6, 4), (6, 6), (4, 6)]]
        planner = GridPlanner(box, "1/2", bounds, step=1)
        plan = planner.plan((1, 1), (9, 9))
        reports = validate_path(plan.waypoints, box, "1/2", bounds)
        self.assertTrue(all(r.ok for r in reports))
        for r in reports:
            self.assertGreaterEqual(r.min_clearance2, F(1, 4))  # exact >= r^2
        # the straight diagonal is blocked, so the path must detour
        self.assertGreaterEqual(len(plan.waypoints), 3)

    def test_diagonal_pinch_blocks_robot_with_radius(self):
        bounds = Bounds.of(0, 0, 12, 12)
        obstacles = [
            [(0, 5), (5, 5), (5, 7), (0, 7)],
            [(5, 3), (12, 3), (12, 5), (5, 5)],
        ]
        planner = GridPlanner(obstacles, "1/2", bounds, step=1)
        with self.assertRaises(NoPathError):
            planner.plan((6, 10), (6, 1))

    def test_diagonal_pinch_point_robot_passes(self):
        bounds = Bounds.of(0, 0, 12, 12)
        obstacles = [
            [(0, 5), (5, 5), (5, 7), (0, 7)],
            [(5, 3), (12, 3), (12, 5), (5, 5)],
        ]
        planner = GridPlanner(obstacles, 0, bounds, step=1)
        plan = planner.plan((6, 10), (6, 1))
        self.assertTrue(path_valid(plan.waypoints, obstacles, 0, bounds))

    def test_exact_gap_contact_allowed(self):
        # gap of 3/2 between obstacles; radius 1/2 -> corridor center has
        # clearance exactly 1/2 == radius and must be accepted (exact >=).
        bounds = Bounds.of(0, 0, 12, 12)
        obstacles = [
            [(0, 5), (5, 5), (5, 7), (0, 7)],
            [(5, "3/2"), (12, "3/2"), (12, "7/2"), (5, "7/2")],
        ]
        planner = GridPlanner(obstacles, "1/2", bounds, step=1)
        plan = planner.plan((6, 10), (6, 1))
        reports = validate_path(plan.waypoints, obstacles, "1/2", bounds)
        self.assertTrue(all(r.ok for r in reports))
        best = min(r.min_clearance2 for r in reports)
        self.assertEqual(best, F(1, 4))  # exactly radius^2, not an epsilon away

    def test_start_covered_by_inflated_obstacle(self):
        bounds = Bounds.of(0, 0, 10, 10)
        box = [[(2, 2), (4, 2), (4, 4), (2, 4)]]
        planner = GridPlanner(box, "3/2", bounds, step=1)
        with self.assertRaises(NoPathError):
            planner.plan((1, 3), (9, 9))  # clearance 1 < 3/2

    def test_start_inside_obstacle(self):
        bounds = Bounds.of(0, 0, 10, 10)
        box = [[(2, 2), (4, 2), (4, 4), (2, 4)]]
        planner = GridPlanner(box, 0, bounds, step=1)
        with self.assertRaises(NoPathError):
            planner.plan((3, 3), (9, 9))

    def test_goal_covered_by_inflated_obstacle(self):
        bounds = Bounds.of(0, 0, 10, 10)
        box = [[(2, 2), (4, 2), (4, 4), (2, 4)]]
        planner = GridPlanner(box, 1, bounds, step=1)
        with self.assertRaises(NoPathError):
            planner.plan((9, 9), ("9/2", 3))  # clearance 1/2 < radius 1

    def test_simplification_revalidates_clearance(self):
        # wall from the bottom edge up to y=5 forces a detour over the top;
        # the naive straight shortcut start->goal crosses the wall.
        bounds = Bounds.of(0, 0, 10, 10)
        wall = [[(4, 0), (6, 0), (6, 5), (4, 5)]]
        planner = GridPlanner(wall, "1/2", bounds, step=1)
        plan = planner.plan((1, 1), (9, 1))
        self.assertGreaterEqual(len(plan.waypoints), 3)
        # the naive shortcut is indeed invalid...
        naive = validate_path([P(1, 1), P(9, 1)], wall, "1/2", bounds)
        self.assertFalse(all(r.ok for r in naive))
        # ...but every simplified segment passes the exact re-check
        reports = validate_path(plan.waypoints, wall, "1/2", bounds)
        self.assertTrue(all(r.ok for r in reports))
        self.assertLess(len(plan.waypoints), len(plan.grid_path))

    def test_rational_coordinates(self):
        bounds = Bounds.of(0, 0, 8, 8)
        tri = [[("5/2", "5/2"), ("11/2", "5/2"), ("4", "9/2")]]
        planner = GridPlanner(tri, "1/3", bounds, step="1/2")
        plan = planner.plan(("1/2", "1/2"), (7, 7))
        reports = validate_path(plan.waypoints, tri, "1/3", bounds)
        self.assertTrue(all(r.ok for r in reports))
        for r in reports:
            self.assertGreaterEqual(r.min_clearance2, F(1, 9))

    def test_validator_rejects_handmade_bad_path(self):
        bounds = Bounds.of(0, 0, 10, 10)
        box = [[(4, 4), (6, 4), (6, 6), (4, 6)]]
        bad = [to_point((1, 5)), to_point((9, 5))]  # straight through the box
        reports = validate_path(bad, box, "1/2", bounds)
        self.assertFalse(all(r.ok for r in reports))
        self.assertTrue(reports[0].enters_interior)

    def test_no_float_needed_anywhere_in_result(self):
        bounds = Bounds.of(0, 0, 6, 6)
        planner = GridPlanner([], "1/2", bounds, step=1)
        plan = planner.plan((1, 1), (5, 5))
        for p in plan.waypoints:
            self.assertIsInstance(p[0], Fraction)
            self.assertIsInstance(p[1], Fraction)


if __name__ == "__main__":
    unittest.main(verbosity=2)
