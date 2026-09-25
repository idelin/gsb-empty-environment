import unittest
from fractions import Fraction

from exactgeom import orient, segments_intersect
from polysimplify import simplify_polylines

F = Fraction


def signed_area2_sign(pts):
    s = sum(pts[i][0] * pts[(i + 1) % len(pts)][1]
            - pts[(i + 1) % len(pts)][0] * pts[i][1]
            for i in range(len(pts)))
    return (s > 0) - (s < 0)


def has_new_intersection(pts, closed):
    """True iff the polyline has a proper self-crossing."""
    n = len(pts)
    m = n if closed else n - 1
    segs = [(pts[i], pts[(i + 1) % n]) for i in range(m)
            if pts[i] != pts[(i + 1) % n]]
    for i in range(len(segs)):
        for j in range(i + 1, len(segs)):
            a, b = segs[i]
            c, d = segs[j]
            if {a, b} & {c, d}:
                continue  # adjacent segments may share an endpoint
            if segments_intersect(a, b, c, d):
                return True
    return False


class TestThresholdZero(unittest.TestCase):
    def test_removes_only_strictly_redundant(self):
        # (1,0) and (3,1) are exactly on the segments of their neighbours.
        p = [(0, 0), (1, 0), (2, 0), (3, 1), (4, 2)]
        r = simplify_polylines([p], threshold=0)
        self.assertEqual(r.polylines[0],
                         [(F(0), F(0)), (F(2), F(0)), (F(4), F(2))])
        self.assertEqual(r.max_error2, 0)

    def test_keeps_non_redundant_points(self):
        p = [(0, 0), (1, 1), (2, 0)]
        r = simplify_polylines([p], threshold=0)
        self.assertEqual(len(r.polylines[0]), 3)
        self.assertEqual(len(r.removed), 0)

    def test_removes_duplicate_points(self):
        p = [(0, 0), (1, 1), (1, 1), (2, 0)]
        r = simplify_polylines([p], threshold=0)
        self.assertEqual(r.polylines[0],
                         [(F(0), F(0)), (F(1), F(1)), (F(2), F(0))])

    def test_closed_ring_duplicate_vertex(self):
        ring = [(0, 0), (2, 0), (2, 0), (1, 1)]
        r = simplify_polylines([ring], threshold=0, closed=[True])
        self.assertEqual(len(r.polylines[0]), 3)
        self.assertEqual(signed_area2_sign(r.polylines[0]), 1)


class TestSharedSeam(unittest.TestCase):
    LEFT = [(0, 0), (10, 0), (10, 4), (11, 5), (10, 6), (10, 10), (0, 10)]
    RIGHT = [(10, 0), (20, 0), (20, 10), (10, 10), (10, 6), (11, 5), (10, 4)]

    def test_seam_simplified_identically(self):
        r = simplify_polylines([self.LEFT, self.RIGHT],
                               threshold=F(3, 2), closed=[True, True])
        self.assertEqual(r.polylines[0],
                         [(F(0), F(0)), (F(10), F(0)), (F(10), F(10)),
                          (F(0), F(10))])
        self.assertEqual(r.polylines[1],
                         [(F(10), F(0)), (F(20), F(0)), (F(20), F(10)),
                          (F(10), F(10))])
        # certified error of every removed point respects the threshold
        for rp in r.removed:
            self.assertLessEqual(rp.dist2_to_result, r.threshold2)
        self.assertLessEqual(r.max_error2, r.threshold2)

    def test_seam_kept_identically_below_threshold(self):
        r = simplify_polylines([self.LEFT, self.RIGHT],
                               threshold=F(1, 2), closed=[True, True])
        # Nothing may be removed: every candidate error exceeds 1/2.
        self.assertEqual(len(r.removed), 0)
        # Both sides still describe the exact same seam vertices.
        seam = [(F(10), F(0)), (F(10), F(4)), (F(11), F(5)), (F(10), F(6)),
                (F(10), F(10))]
        seam_l = [p for p in r.polylines[0] if p[0] >= F(10)]
        right = r.polylines[1]
        seam_r = right[3:] + right[:1]  # contiguous seam run, wrapping
        self.assertEqual(seam_l, seam)
        self.assertEqual(seam_r, seam[::-1])


class TestNarrowChannel(unittest.TestCase):
    WALL_A = [(0, 0), (5, 0), (5, 5)]
    WALL_B = [(4, 6), (F(9, 2), F(1, 2))]

    def test_channel_not_sealed(self):
        # Cutting the corner (5,0) of wall A is well within threshold 10
        # (error^2 = 25/2) but the chord (0,0)-(5,5) would cross wall B.
        r = simplify_polylines([self.WALL_A, self.WALL_B], threshold=10)
        self.assertEqual(len(r.polylines[0]), 3)  # corner kept
        self.assertEqual(len(r.removed), 0)

    def test_same_cut_allowed_without_obstacle(self):
        r = simplify_polylines([self.WALL_A], threshold=10)
        self.assertEqual(r.polylines[0], [(F(0), F(0)), (F(5), F(5))])
        self.assertEqual(r.max_error2, F(25, 2))


class TestRings(unittest.TestCase):
    def test_orientation_and_shape_preserved(self):
        ring = [(0, 0), (10, 0), (10, 4), (11, 5), (10, 6), (10, 10), (0, 10)]
        r = simplify_polylines([ring], threshold=2, closed=[True])
        out = r.polylines[0]
        self.assertEqual(signed_area2_sign(out), signed_area2_sign(ring))
        self.assertGreaterEqual(len(out), 3)
        self.assertFalse(has_new_intersection(out, True))
        self.assertEqual(out, [(F(0), F(0)), (F(10), F(0)), (F(10), F(10)),
                               (F(0), F(10))])

    def test_triangle_ring_never_collapses(self):
        ring = [(0, 0), (4, 0), (2, 3)]
        r = simplify_polylines([ring], threshold=1000, closed=[True])
        self.assertEqual(len(r.polylines[0]), 3)

    def test_ring_with_duplicated_closing_point(self):
        ring = [(0, 0), (4, 0), (2, 3), (0, 0)]  # duplicated closure dropped
        r = simplify_polylines([ring], threshold=1000, closed=[True])
        self.assertEqual(len(r.polylines[0]), 3)


class TestErrorBound(unittest.TestCase):
    def test_certified_bound_exact(self):
        p = [(0, 0), (1, 1), (2, 0), (3, 1), (4, 0)]
        r = simplify_polylines([p], threshold=1)
        self.assertEqual(r.polylines[0], [(F(0), F(0)), (F(4), F(0))])
        self.assertEqual(r.max_error2, F(1))
        errs = sorted(rp.dist2_to_result for rp in r.removed)
        self.assertEqual(errs, [F(0), F(1), F(1)])
        for rp in r.removed:
            self.assertLessEqual(rp.dist2_to_result, r.threshold2)

    def test_rational_coordinates_exact(self):
        p = [(0, 0), (F(1, 3), F(1, 2)), (1, 0)]
        r = simplify_polylines([p], threshold=1)
        self.assertEqual(r.polylines[0], [(F(0), F(0)), (F(1), F(0))])
        self.assertEqual(r.max_error2, F(1, 4))
        self.assertIsInstance(r.max_error2, Fraction)


class TestJunctions(unittest.TestCase):
    def test_junction_vertex_never_removed(self):
        a = [(0, 0), (5, 0), (10, 0)]
        b = [(5, 0), (5, 5)]
        r = simplify_polylines([a, b], threshold=0)
        self.assertEqual(len(r.polylines[0]), 3)  # (5,0) is a T-junction

    def test_t_junction_on_unchanged_geometry_ok(self):
        # (4,0) is strictly redundant; the chord is geometrically identical,
        # so B's vertex (2,0) lying on it must not block the removal.
        a = [(0, 0), (4, 0), (8, 0)]
        b = [(2, 0), (2, -5)]
        r = simplify_polylines([a, b], threshold=0)
        self.assertEqual(r.polylines[0], [(F(0), F(0)), (F(8), F(0))])
        self.assertEqual(len(r.polylines[1]), 2)


class TestDegenerate(unittest.TestCase):
    def test_empty_and_tiny_inputs(self):
        r = simplify_polylines([[], [(1, 2)], [(0, 0), (1, 1)]], threshold=5)
        self.assertEqual(r.polylines[0], [])
        self.assertEqual(r.polylines[1], [(F(1), F(2))])
        self.assertEqual(len(r.polylines[2]), 2)

    def test_degenerate_ring_unchanged(self):
        r = simplify_polylines([[(0, 0), (1, 1)]], threshold=5, closed=[True])
        self.assertEqual(len(r.polylines[0]), 2)

    def test_negative_threshold_rejected(self):
        with self.assertRaises(ValueError):
            simplify_polylines([[(0, 0), (1, 1)]], threshold=-1)

    def test_string_and_int_coordinates(self):
        p = [("0", "0"), ("1/2", "1/3"), ("1", "0")]
        r = simplify_polylines([p], threshold=1)
        self.assertEqual(r.polylines[0], [(F(0), F(0)), (F(1), F(0))])
        self.assertEqual(r.max_error2, F(1, 9))


if __name__ == "__main__":
    unittest.main()
