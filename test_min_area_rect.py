"""Unittest suite for min_area_rect (exact arithmetic, stdlib only)."""

import random
import unittest
from fractions import Fraction

from min_area_rect import (
    PointSet,
    brute_force_min_area,
    convex_hull,
    min_area_rectangle,
    polygon_area,
    to_point,
    verify_certificate,
)


def P(x, y):
    return (Fraction(x), Fraction(y))


class TestConvexHull(unittest.TestCase):
    def test_single_point(self):
        self.assertEqual(convex_hull([P(3, 4), P(3, 4)]), [P(3, 4)])

    def test_collinear_collapses_to_endpoints(self):
        hull = convex_hull([P(0, 0), P(1, 1), P(2, 2), P(3, 3)])
        self.assertEqual(hull, [P(0, 0), P(3, 3)])

    def test_interior_points_removed(self):
        hull = convex_hull([P(0, 0), P(4, 0), P(4, 4), P(0, 4), P(2, 2), P(1, 1)])
        self.assertEqual(len(hull), 4)


class TestDegenerate(unittest.TestCase):
    def test_empty_raises(self):
        with self.assertRaises(ValueError):
            min_area_rectangle([])
        with self.assertRaises(ValueError):
            brute_force_min_area([])
        with self.assertRaises(ValueError):
            PointSet().minimum_area_rectangle()

    def test_single_point_zero_area(self):
        cert = min_area_rectangle([(5, -7)])
        self.assertEqual(cert.area, 0)
        self.assertEqual(cert.corners, (P(5, -7),) * 4)
        self.assertEqual(cert.u_range, (Fraction(5), Fraction(5)))
        self.assertEqual(cert.v_range, (Fraction(-7), Fraction(-7)))
        self.assertTrue(verify_certificate(cert, [(5, -7)]))

    def test_all_identical_points(self):
        cert = min_area_rectangle([(2, 2)] * 5)
        self.assertEqual(cert.area, 0)

    def test_two_points_zero_area(self):
        cert = min_area_rectangle([(0, 0), (3, 4)])
        self.assertEqual(cert.area, 0)
        self.assertTrue(verify_certificate(cert, [(0, 0), (3, 4)]))

    def test_collinear_zero_area_no_negative(self):
        pts = [(k, 2 * k) for k in range(-10, 11)]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert.area, 0)
        self.assertGreaterEqual(cert.area, 0)
        self.assertTrue(verify_certificate(cert, pts))


class TestKnownGeometry(unittest.TestCase):
    def test_axis_aligned_square(self):
        pts = [(0, 0), (2, 0), (2, 2), (0, 2)]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert.area, 4)
        self.assertEqual(cert.direction, P(2, 0))
        self.assertTrue(verify_certificate(cert, pts))

    def test_rotated_square_is_its_own_minimum(self):
        pts = [(0, 0), (1, 1), (0, 2), (-1, 1)]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert.area, 2)
        self.assertTrue(verify_certificate(cert, pts))

    def test_rectangle_beats_axis_frame(self):
        # This quadrilateral is itself a rectangle (perpendicular sides
        # (6,2) and (-2,6)); its own frame beats the axis-aligned one.
        pts = [(0, 0), (6, 2), (4, 8), (-2, 6)]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert.area, 40)
        axis_frame = 8 * 8
        self.assertLess(cert.area, axis_frame)
        self.assertTrue(verify_certificate(cert, pts))

    def test_rational_coordinates(self):
        pts = [(Fraction(1, 3), Fraction(1, 2)), (Fraction(5, 6), Fraction(-1, 4)),
               (Fraction(-2, 3), Fraction(7, 8)), (0, 0)]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert, brute_force_min_area(pts))
        self.assertTrue(verify_certificate(cert, pts))

    def test_corners_recover_equal_area_rectangle(self):
        pts = [(1, 0), (0, 3), (5, 5), (2, 2), (-1, 4)]
        cert = min_area_rectangle(pts)
        self.assertEqual(polygon_area(cert.corners), cert.area)
        # Opposite sides equal and adjacent sides perpendicular (exact).
        c = cert.corners
        s1 = (c[1][0] - c[0][0], c[1][1] - c[0][1])
        s2 = (c[2][0] - c[1][0], c[2][1] - c[1][1])
        s3 = (c[3][0] - c[2][0], c[3][1] - c[2][1])
        s4 = (c[0][0] - c[3][0], c[0][1] - c[3][1])
        self.assertEqual(s1, (-s3[0], -s3[1]))
        self.assertEqual(s2, (-s4[0], -s4[1]))
        self.assertEqual(s1[0] * s2[0] + s1[1] * s2[1], 0)


class TestExactnessLargeCoordinates(unittest.TestCase):
    def test_near_collinear_large_integers(self):
        # Nearly collinear at 1e9 scale: float arithmetic would drown the
        # height-1 deviation; exact arithmetic keeps the area exact.
        pts = [(0, 0), (10**9, 0), (10**9, 1), (0, 1), (5 * 10**8, 0)]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert.area, 10**9)
        self.assertGreaterEqual(cert.area, 0)
        self.assertTrue(verify_certificate(cert, pts))

    def test_near_collinear_tilted_large_integers(self):
        # Points on y = x plus one unit-offset point, shifted by 1e9.
        base = 10**9
        pts = [(base + k, base + k) for k in range(0, 1000, 100)]
        pts.append((base + 500, base + 501))
        cert = min_area_rectangle(pts)
        self.assertGreaterEqual(cert.area, 0)
        self.assertEqual(cert, brute_force_min_area(pts))
        self.assertTrue(verify_certificate(cert, pts))

    def test_no_epsilon_area_comparison(self):
        # Two candidate frames whose areas differ by a tiny exact amount;
        # the winner must be decided exactly, never by an epsilon.
        pts = [(0, 0), (10**6, 1), (10**6 - 1, 10**6), (-1, 10**6 - 1)]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert, brute_force_min_area(pts))
        self.assertTrue(verify_certificate(cert, pts))


class TestTieBreaking(unittest.TestCase):
    def test_square_picks_zero_angle(self):
        # Axis-aligned and 90-degree frames tie; canonical rule keeps angle 0.
        pts = [(0, 0), (2, 0), (2, 2), (0, 2)]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert.direction, P(2, 0))

    def test_input_order_invariance(self):
        rng = random.Random(42)
        for _ in range(100):
            pts = [(rng.randint(-20, 20), rng.randint(-20, 20))
                   for _ in range(rng.randint(3, 10))]
            shuffled = pts[:]
            rng.shuffle(shuffled)
            self.assertEqual(min_area_rectangle(pts), min_area_rectangle(shuffled))

    def test_duplicate_coordinates_same_certificate(self):
        pts = [(0, 0), (3, 0), (3, 1), (0, 1)]
        dup = pts + [(0, 0), (3, 1), (3, 1)]
        self.assertEqual(min_area_rectangle(pts), min_area_rectangle(dup))

    def test_regular_hexagon_deterministic(self):
        # Many equal-area optima; the canonical choice must be stable.
        pts = [(2, 0), (1, 2), (-1, 2), (-2, 0), (-1, -2), (1, -2)]
        c1 = min_area_rectangle(pts)
        c2 = min_area_rectangle(list(reversed(pts)))
        self.assertEqual(c1, c2)
        self.assertTrue(verify_certificate(c1, pts))


class TestPointSetMutation(unittest.TestCase):
    def test_batch_withdrawal(self):
        ps = PointSet([(0, 0), (4, 0), (4, 3), (0, 3), (100, 100)])
        ids = list(ps._pts)
        far_id = ids[-1]
        self.assertEqual(ps.minimum_area_rectangle(),
                         brute_force_min_area(ps.points()))
        ps.remove_many([far_id])
        cert = ps.minimum_area_rectangle()
        self.assertEqual(cert.area, 12)
        self.assertTrue(verify_certificate(cert, ps.points()))

    def test_batch_withdrawal_multiple(self):
        ps = PointSet([(0, 0), (10, 0), (10, 10), (0, 10), (5, 5), (1, 1)])
        ids = list(ps._pts)
        ps.remove_many(ids[4:])  # withdraw interior points in one batch
        self.assertEqual(ps.minimum_area_rectangle().area, 100)
        ps.remove_many(ids[1:3])
        self.assertEqual(len(ps), 2)
        self.assertEqual(ps.minimum_area_rectangle().area, 0)

    def test_unknown_id_raises(self):
        ps = PointSet([(0, 0)])
        with self.assertRaises(KeyError):
            ps.remove_many([999])

    def test_duplicate_identity(self):
        ps = PointSet([(0, 0), (2, 0), (2, 2), (0, 2)])
        dup_id = ps.add((0, 2))  # duplicate coordinate, new identity
        self.assertEqual(ps.minimum_area_rectangle().area, 4)
        ps.remove_many([dup_id])  # withdraw only the duplicate
        self.assertEqual(len(ps), 4)
        self.assertEqual(ps.minimum_area_rectangle().area, 4)

    def test_remove_unique_extreme_point(self):
        ps = PointSet([(0, 0), (6, 0), (6, 4), (0, 4), (3, 2)])
        ids = list(ps._pts)
        before = ps.minimum_area_rectangle()
        self.assertEqual(before.area, 24)
        ps.remove_many([ids[2]])  # (6,4): unique top-right extreme
        cert = ps.minimum_area_rectangle()
        self.assertNotEqual(cert, before)
        self.assertEqual(cert, min_area_rectangle([(0, 0), (6, 0), (0, 4), (3, 2)]))
        self.assertTrue(verify_certificate(cert, ps.points()))
        ps.remove_many([ids[0], ids[1], ids[3]])
        self.assertEqual(ps.minimum_area_rectangle().area, 0)  # one point left


class TestCrossValidation(unittest.TestCase):
    def test_random_sets_match_brute_force(self):
        rng = random.Random(2026)
        for _ in range(300):
            pts = [(rng.randint(-100, 100), rng.randint(-100, 100))
                   for _ in range(rng.randint(1, 15))]
            cert = min_area_rectangle(pts)
            self.assertEqual(cert, brute_force_min_area(pts))
            self.assertTrue(verify_certificate(cert, pts))

    def test_random_rational_sets_match_brute_force(self):
        rng = random.Random(99)
        for _ in range(150):
            pts = [(Fraction(rng.randint(-30, 30), rng.randint(1, 6)),
                    Fraction(rng.randint(-30, 30), rng.randint(1, 6)))
                   for _ in range(rng.randint(1, 10))]
            cert = min_area_rectangle(pts)
            self.assertEqual(cert, brute_force_min_area(pts))
            self.assertTrue(verify_certificate(cert, pts))

    def test_certificate_rejects_wrong_area(self):
        pts = [(0, 0), (4, 0), (4, 3), (0, 3)]
        cert = min_area_rectangle(pts)
        bad = type(cert)(**{**cert.__dict__, "area": cert.area + 1})
        self.assertFalse(verify_certificate(bad, pts))


if __name__ == "__main__":
    unittest.main()
