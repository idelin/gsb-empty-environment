"""Unit tests for topology-preserving polyline network simplification."""

import unittest
from fractions import Fraction

from geom import CROSS, NONE, OVERLAP, TOUCH, segments_relation, twice_signed_area
from simplify import simplify_network

F = Fraction


def segs_of(poly):
    return list(zip(poly, poly[1:]))


def assert_network_simple(testcase, polylines):
    """No proper crossings, no partial overlaps, no T-touches anywhere."""
    segs = []
    for poly in polylines:
        segs.extend(segs_of(poly))
    for i, (a, b) in enumerate(segs):
        for c, d in segs[i + 1:]:
            rel = segments_relation(a, b, c, d)
            kind = rel[0]
            if kind in (NONE,):
                continue
            if kind == CROSS:
                testcase.fail(f"new crossing: {a}-{b} x {c}-{d}")
            if kind == OVERLAP:
                # Only fully coincident shared-boundary edges are allowed.
                testcase.assertEqual(
                    {a, b}, {c, d}, f"partial overlap: {a}-{b} vs {c}-{d}"
                )
            if kind == TOUCH:
                for t in rel[1]:
                    ok = t in (a, b) and t in (c, d)
                    testcase.assertTrue(
                        ok, f"T-touch at {t}: {a}-{b} vs {c}-{d}"
                    )


def boundary_between(poly, p, q):
    """Subsequence of poly between points p and q (inclusive)."""
    i = poly.index(p)
    j = poly.index(q)
    if i <= j:
        return poly[i:j + 1]
    return poly[i:] + poly[1:j + 1]


class TestExactBasics(unittest.TestCase):
    def test_tol_zero_removes_only_strictly_redundant(self):
        poly = [(0, 0), (1, 0), (2, 0), (3, 1), (4, 1)]
        res = simplify_network([poly], tol=0)
        # (1,0) is exactly collinear between (0,0) and (2,0): redundant.
        self.assertEqual(
            res.polylines[0],
            [(F(0), F(0)), (F(2), F(0)), (F(3), F(1)), (F(4), F(1))],
        )
        self.assertEqual(len(res.removed), 1)
        self.assertEqual(res.removed[0].error_sq, F(0))

    def test_tol_zero_keeps_noncollinear_points(self):
        poly = [(0, 0), (1, 1), (2, 0)]
        res = simplify_network([poly], tol=0)
        self.assertEqual(len(res.polylines[0]), 3)
        self.assertEqual(res.removed, [])

    def test_duplicate_points_removed_at_tol_zero(self):
        poly = [(0, 0), (0, 0), (1, 0), (1, 0), (2, 0)]
        res = simplify_network([poly], tol=0)
        self.assertEqual(res.polylines[0], [(F(0), F(0)), (F(2), F(0))])

    def test_rational_coordinates_exact(self):
        poly = [(0, 0), (1, F(1, 3)), (2, 0)]
        res = simplify_network([poly], tol=F(1, 2))
        self.assertEqual(res.polylines[0], [(F(0), F(0)), (F(2), F(0))])
        self.assertEqual(res.removed[0].error_sq, F(1, 9))
        # tol just below the exact error must keep the point
        res2 = simplify_network([poly], tol=F(1, 4))
        self.assertEqual(len(res2.polylines[0]), 3)

    def test_float_coordinates_rejected(self):
        with self.assertRaises(TypeError):
            simplify_network([[(0.0, 0.0), (1.0, 1.0)]], tol=0)


class TestDegenerate(unittest.TestCase):
    def test_empty_polyline(self):
        res = simplify_network([[]], tol=1)
        self.assertEqual(res.polylines[0], [])

    def test_single_point(self):
        res = simplify_network([[(3, 4)]], tol=1)
        self.assertEqual(res.polylines[0], [(F(3), F(4))])

    def test_two_points_unchanged(self):
        res = simplify_network([[(0, 0), (5, 5)]], tol=100)
        self.assertEqual(len(res.polylines[0]), 2)

    def test_all_identical_ring_collapses_to_point(self):
        res = simplify_network([[(1, 1), (1, 1), (1, 1), (1, 1)]], tol=0)
        self.assertEqual(res.polylines[0], [(F(1), F(1))])

    def test_zero_area_ring_never_flips(self):
        # Collinear closed ring: orientation undefined, must stay simple.
        ring = [(0, 0), (2, 0), (4, 0), (0, 0)]
        res = simplify_network([ring], tol=10)
        out = res.polylines[0]
        assert_network_simple(self, [out])


class TestSharedBoundaries(unittest.TestCase):
    def setUp(self):
        # Two regions sharing the wiggly boundary x~2:
        # (2,0)-(3,1)-(2,2)-(3,3)-(2,4)
        self.region_a = [
            (0, 0), (2, 0), (3, 1), (2, 2), (3, 3), (2, 4), (0, 4), (0, 0),
        ]
        self.region_b = [
            (2, 0), (4, 0), (4, 4), (2, 4), (3, 3), (2, 2), (3, 1), (2, 0),
        ]

    def test_shared_boundary_identical_after_simplification(self):
        res = simplify_network([self.region_a, self.region_b], tol=1)
        a, b = res.polylines
        p2, p4 = (F(2), F(0)), (F(2), F(4))
        bnd_a = boundary_between(a, p2, p4)
        bnd_b = boundary_between(b, p4, p2)
        self.assertEqual(bnd_a, list(reversed(bnd_b)))
        # wiggles of amplitude 1 are within tol=1 of the straight line x=2
        self.assertEqual(bnd_a, [p2, p4])
        assert_network_simple(self, res.polylines)

    def test_shared_boundary_identical_at_tol_zero(self):
        res = simplify_network([self.region_a, self.region_b], tol=0)
        a, b = res.polylines
        p2, p4 = (F(2), F(0)), (F(2), F(4))
        bnd_a = boundary_between(a, p2, p4)
        bnd_b = boundary_between(b, p4, p2)
        self.assertEqual(bnd_a, list(reversed(bnd_b)))
        # Nothing on the wiggly boundary is strictly redundant.
        self.assertEqual(len(bnd_a), 5)

    def test_shared_collinear_midpoint_removed_from_both(self):
        sq_a = [(0, 0), (2, 0), (2, 1), (2, 2), (0, 2), (0, 0)]
        sq_b = [(2, 0), (4, 0), (4, 2), (2, 2), (2, 1), (2, 0)]
        res = simplify_network([sq_a, sq_b], tol=0)
        a, b = res.polylines
        self.assertNotIn((F(2), F(1)), a)
        self.assertNotIn((F(2), F(1)), b)
        bnd_a = boundary_between(a, (F(2), F(0)), (F(2), F(2)))
        bnd_b = boundary_between(b, (F(2), F(2)), (F(2), F(0)))
        self.assertEqual(bnd_a, list(reversed(bnd_b)))

    def test_partial_shared_boundary_t_junctions_kept(self):
        big = [(0, 0), (4, 0), (4, 3), (0, 3), (0, 0)]
        small = [(1, 0), (3, 0), (3, -2), (1, -2), (1, 0)]
        res = simplify_network([big, small], tol=10)
        big_out, small_out = res.polylines
        # T-junctions (1,0) and (3,0) are topological features: kept.
        self.assertIn((F(1), F(0)), big_out)
        self.assertIn((F(3), F(0)), big_out)
        assert_network_simple(self, res.polylines)

    def test_grid_of_four_regions(self):
        # 2x2 cells sharing wiggly internal boundaries.
        mid_v = [(2, 0), (3, 1), (2, 2), (1, 3), (2, 4)]       # x~2
        mid_h = [(0, 2), (1, 1), (2, 2), (3, 3), (4, 2)]       # y~2
        sw = [(0, 0)] + mid_v[:3] + mid_h[1::-1] + [(0, 0)]
        se = mid_v[:3] + [(4, 0), (4, 2)] + mid_h[3:2:-1] + [mid_v[2]]
        nw = mid_h[:3][::-1] + [(0, 4)] + mid_v[2:][::-1] + [mid_h[2]]
        ne = [mid_v[2]] + mid_v[3:] + [(4, 4)] + mid_h[3:][::-1] + [mid_v[2]]
        cells = [sw, se, nw, ne]
        res = simplify_network(cells, tol=1)
        assert_network_simple(self, res.polylines)
        # The shared vertex (2,2) survives identically in all four cells.
        for cell in res.polylines:
            self.assertIn((F(2), F(2)), cell)
        for r in res.removed:
            self.assertLessEqual(r.error_sq, 1)


class TestTopology(unittest.TestCase):
    def test_narrow_channel_not_sealed(self):
        # Arch over a vertical wall: the naive chord (0,0)-(12,0) would
        # cross the wall and seal the channel.
        arch = [(0, 0), (4, 2), (8, 2), (12, 0)]
        wall = [(6, -1), (6, 1)]
        res = simplify_network([arch, wall], tol=3)
        arch_out, wall_out = res.polylines
        self.assertEqual(wall_out, [(F(6), F(-1)), (F(6), F(1))])
        # The arch must not collapse onto y=0 across the wall.
        self.assertTrue(any(p[1] > 0 for p in arch_out))
        assert_network_simple(self, res.polylines)

    def test_ring_orientation_and_min_vertices(self):
        # Flat triangle: naive DP with tol=2 would collapse it to 2 points.
        tri = [(0, 0), (4, 0), (2, 1), (0, 0)]
        res = simplify_network([tri], tol=2)
        out = res.polylines[0]
        self.assertEqual(out[0], out[-1])
        self.assertGreaterEqual(len(out) - 1, 3)
        self.assertGreater(twice_signed_area(out[:-1]), 0)

    def test_square_ring_stays_simple_under_huge_tol(self):
        sq = [(0, 0), (1, 0), (2, 0), (2, 2), (0, 2), (0, 0)]
        res = simplify_network([sq], tol=100)
        out = res.polylines[0]
        self.assertGreaterEqual(len(out) - 1, 3)
        self.assertGreater(twice_signed_area(out[:-1]), 0)
        assert_network_simple(self, res.polylines)

    def test_reversed_ring_orientation_preserved(self):
        tri = [(0, 0), (2, 1), (4, 0), (0, 0)]  # clockwise
        res = simplify_network([tri], tol=2)
        out = res.polylines[0]
        self.assertLess(twice_signed_area(out[:-1]), 0)


class TestErrorBounds(unittest.TestCase):
    def test_removed_points_within_tolerance(self):
        poly = [(0, 0), (1, 2), (2, 0), (3, 2), (4, 0)]
        res = simplify_network([poly], tol=3)
        self.assertEqual(res.polylines[0], [(F(0), F(0)), (F(4), F(0))])
        self.assertEqual(len(res.removed), 3)
        for r in res.removed:
            self.assertLessEqual(r.error_sq, F(9))
        self.assertEqual(res.max_error_sq, F(4))

    def test_error_bound_holds_on_network(self):
        region_a = [(0, 0), (2, 0), (3, 1), (2, 2), (3, 3), (2, 4), (0, 4), (0, 0)]
        region_b = [(2, 0), (4, 0), (4, 4), (2, 4), (3, 3), (2, 2), (3, 1), (2, 0)]
        res = simplify_network([region_a, region_b], tol=F(3, 2))
        for r in res.removed:
            self.assertLessEqual(r.error_sq, F(9, 4))
        self.assertLessEqual(res.max_error_sq, F(9, 4))


if __name__ == "__main__":
    unittest.main()
