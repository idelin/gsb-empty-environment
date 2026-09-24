from fractions import Fraction
import unittest

from bezier_flatten import FlattenError, flatten_bezier, flatten_bezier_intervals


def quadratic(points, t):
    u = 1 - t
    return (
        u * u * points[0][0] + 2 * u * t * points[1][0] + t * t * points[2][0],
        u * u * points[0][1] + 2 * u * t * points[1][1] + t * t * points[2][1],
    )


def cubic(points, t):
    u = 1 - t
    return (
        u**3 * points[0][0]
        + 3 * u * u * t * points[1][0]
        + 3 * u * t * t * points[2][0]
        + t**3 * points[3][0],
        u**3 * points[0][1]
        + 3 * u * u * t * points[1][1]
        + 3 * u * t * t * points[2][1]
        + t**3 * points[3][1],
    )


def distance_squared_to_segment(point, start, end):
    vx = end[0] - start[0]
    vy = end[1] - start[1]
    wx = point[0] - start[0]
    wy = point[1] - start[1]
    length_squared = vx * vx + vy * vy
    if length_squared == 0:
        return wx * wx + wy * wy

    ratio = max(0, min(1, (wx * vx + wy * vy) / length_squared))
    closest_x = start[0] + ratio * vx
    closest_y = start[1] + ratio * vy
    dx = point[0] - closest_x
    dy = point[1] - closest_y
    return dx * dx + dy * dy


class FlattenBezierTest(unittest.TestCase):
    def test_straight_quadratic_is_one_segment(self):
        points = [(0, 0), (2, 2), (4, 4)]

        result = flatten_bezier(points, Fraction(1, 10))

        self.assertEqual(flatten_bezier_intervals(points, Fraction(1, 10)), [(0, 1)])
        self.assertEqual(len(result), 1)
        self.assertEqual(result[0].start, (0, 0))
        self.assertEqual(result[0].end, (4, 4))
        self.assertEqual(result[0].squared_bound, 0)

    def test_straight_cubic_is_one_segment(self):
        points = [(0, 0), (1, 0), (3, 0), (4, 0)]

        result = flatten_bezier(points, Fraction(1, 100))

        self.assertEqual(len(result), 1)
        self.assertEqual(flatten_bezier_intervals(points, Fraction(1, 100)), [(0, 1)])
        self.assertEqual(result[0].squared_bound, 0)

    def test_all_control_points_equal_is_degenerate_point(self):
        points = [(Fraction(3, 2), -2)] * 4

        result = flatten_bezier(points, Fraction(1, 1000))

        self.assertEqual(len(result), 1)
        self.assertEqual(result[0].t_start, 0)
        self.assertEqual(result[0].t_end, 1)
        self.assertEqual(result[0].start, result[0].end)
        self.assertEqual(result[0].start, (Fraction(3, 2), -2))
        self.assertEqual(result[0].squared_bound, 0)

    def test_equal_endpoints_with_different_interior_still_subdivides(self):
        points = [(0, 0), (4, 0), (0, 0)]
        tolerance = Fraction(1, 10)

        result = flatten_bezier(points, tolerance, max_depth=40)

        self.assertEqual(result[0].t_start, 0)
        self.assertEqual(result[-1].t_end, 1)
        for left, right in zip(result, result[1:]):
            self.assertEqual(left.t_end, right.t_start)
            self.assertEqual(left.end, right.start)

        for segment in result:
            for numerator in range(21):
                local_t = Fraction(numerator, 20)
                t = segment.t_start + (segment.t_end - segment.t_start) * local_t
                point = quadratic(points, t)
                self.assertLessEqual(
                    distance_squared_to_segment(point, segment.start, segment.end),
                    tolerance * tolerance,
                )

    def test_quadratic_rational_coordinates_are_exact_and_partitioned(self):
        points = [
            (Fraction(0), Fraction(0)),
            (Fraction(1, 2), Fraction(2)),
            (Fraction(2), Fraction(0)),
        ]
        tolerance = Fraction(1, 4)

        result = flatten_bezier(points, tolerance)

        self.assertEqual(result[0].t_start, 0)
        self.assertEqual(result[-1].t_end, 1)
        for left, right in zip(result, result[1:]):
            self.assertEqual(left.t_end, right.t_start)
            self.assertEqual(left.end, right.start)

        for segment in result:
            self.assertLessEqual(segment.squared_bound, tolerance * tolerance)
            for numerator in range(41):
                t = segment.t_start + (segment.t_end - segment.t_start) * Fraction(
                    numerator, 40
                )
                point = quadratic(points, t)
                distance_squared = distance_squared_to_segment(
                    point, segment.start, segment.end
                )
                self.assertLessEqual(distance_squared, tolerance * tolerance)

    def test_quadratic_actual_geometric_error_is_within_tolerance(self):
        points = [(0, 0), (0, 8), (8, 8)]
        tolerance = Fraction(1, 10)

        result = flatten_bezier(points, tolerance)

        for segment in result:
            for numerator in range(101):
                local_t = Fraction(numerator, 100)
                t = segment.t_start + (segment.t_end - segment.t_start) * local_t
                point = quadratic(points, t)
                self.assertLessEqual(
                    distance_squared_to_segment(point, segment.start, segment.end),
                    tolerance * tolerance,
                )

    def test_collinear_overshoot_requires_subdivision(self):
        # The control point lies on the infinite endpoint line, but outside
        # the closed endpoint segment.  Distance to the chord is nonzero, so
        # this must not be accepted merely because all three points are
        # collinear.
        points = [(0, 0), (4, 0), (1, 0)]

        result = flatten_bezier(points, Fraction(1, 100))

        self.assertGreater(len(result), 1)
        for segment in result:
            self.assertLessEqual(
                segment.squared_bound, Fraction(1, 100) ** 2
            )

    def test_cubic_cusp_near_midpoint_keeps_subdividing(self):
        # At t=1/2 this cubic has B'=0 while B'' and B''' are independent;
        # locally it is a semicubical cusp.  A flatness test must continue
        # subdividing through it instead of treating a zero derivative as a
        # reason to stop.
        p0 = (Fraction(0), Fraction(0))
        p1 = (Fraction(1), Fraction(0))
        p2 = (Fraction(0), Fraction(1))
        p3 = (Fraction(1), Fraction(-1))
        points = [p0, p1, p2, p3]

        velocity = tuple(
            Fraction(3, 4)
            * (
                (p1[i] - p0[i])
                + 2 * (p2[i] - p1[i])
                + (p3[i] - p2[i])
            )
            for i in range(2)
        )
        acceleration = tuple(
            3 * (p0[i] - p1[i] - p2[i] + p3[i]) for i in range(2)
        )
        jerk = tuple(
            6 * (p3[i] - 3 * p2[i] + 3 * p1[i] - p0[i])
            for i in range(2)
        )
        self.assertEqual(velocity, (0, 0))
        self.assertNotEqual(
            acceleration[0] * jerk[1] - acceleration[1] * jerk[0],
            0,
        )

        result = flatten_bezier(points, Fraction(1, 100), max_depth=30)

        self.assertEqual(result[0].t_start, 0)
        self.assertEqual(result[-1].t_end, 1)
        self.assertTrue(
            any(
                segment.t_start <= Fraction(1, 2) <= segment.t_end
                for segment in result
            )
        )
        for segment in result:
            self.assertLessEqual(segment.squared_bound, Fraction(1, 100) ** 2)
            for numerator in range(21):
                local_t = Fraction(numerator, 20)
                t = segment.t_start + (segment.t_end - segment.t_start) * local_t
                point = cubic(points, t)
                self.assertLessEqual(
                    distance_squared_to_segment(point, segment.start, segment.end),
                    Fraction(1, 100) ** 2,
                )

    def test_depth_limit_reports_not_met(self):
        points = [(0, 0), (0, 8), (8, 8)]

        with self.assertRaisesRegex(
            FlattenError,
            "error bound not met before depth limit",
        ):
            flatten_bezier(points, Fraction(1, 1_000_000), max_depth=1)

    def test_invalid_arguments_are_rejected(self):
        with self.assertRaises(FlattenError):
            flatten_bezier([(0, 0), (1, 1)], 1)
        with self.assertRaises(FlattenError):
            flatten_bezier([(0, 0), (1, 1), (2, 2)], 0)
        with self.assertRaises(FlattenError):
            flatten_bezier([(0, 0), (1, 1), (2, 2)], -1)
        with self.assertRaises(FlattenError):
            flatten_bezier([(0, 0), (1, 1), (2, 2)], 0.5)
        with self.assertRaises(FlattenError):
            flatten_bezier([(0, 0), (1, 1), (2, 2)], 1, max_depth=-1)


if __name__ == "__main__":
    unittest.main()
