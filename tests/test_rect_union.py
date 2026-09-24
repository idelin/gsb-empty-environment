import random
import unittest
from fractions import Fraction

from rect_union import (
    Rectangle,
    signed_area,
    union,
    union_area,
    union_contours,
    union_perimeter,
)

F = Fraction


def ring(points):
    return tuple((F(x), F(y)) for x, y in points)


def brute_force_area(rects):
    """Independent check: integrate union interval length over x-slabs."""
    rects = [r for r in rects if not r.is_degenerate]
    xs = sorted({r.x1 for r in rects} | {r.x2 for r in rects})
    total = F(0)
    for a, b in zip(xs, xs[1:]):
        intervals = sorted(
            (r.y1, r.y2) for r in rects if r.x1 <= a and b <= r.x2
        )
        covered = F(0)
        cur_lo = cur_hi = None
        for lo, hi in intervals:
            if cur_hi is None or lo > cur_hi:
                if cur_hi is not None:
                    covered += cur_hi - cur_lo
                cur_lo, cur_hi = lo, hi
            else:
                cur_hi = max(cur_hi, hi)
        if cur_hi is not None:
            covered += cur_hi - cur_lo
        total += (b - a) * covered
    return total


def segments_of(ring_pts):
    n = len(ring_pts)
    return [(ring_pts[k], ring_pts[(k + 1) % n]) for k in range(n)]


def segments_touch(e1, e2):
    """Exact intersection test for two axis-aligned closed segments."""
    (ax, ay), (bx, by) = e1
    (cx, cy), (dx, dy) = e2
    return (
        max(min(ax, bx), min(cx, dx)) <= min(max(ax, bx), max(cx, dx))
        and max(min(ay, by), min(cy, dy)) <= min(max(ay, by), max(cy, dy))
    )


def assert_simple_ring(testcase, ring_pts):
    """A simple ring: no repeated vertices, no non-adjacent edge contact."""
    testcase.assertEqual(len(ring_pts), len(set(ring_pts)))
    edges = segments_of(ring_pts)
    n = len(edges)
    for i in range(n):
        for j in range(i + 1, n):
            if j == i or j == (i + 1) % n or i == (j + 1) % n:
                continue
            testcase.assertFalse(
                segments_touch(edges[i], edges[j]),
                f"ring self-touches: {edges[i]} vs {edges[j]}",
            )


class TestRectangle(unittest.TestCase):
    def test_coordinate_order_normalized(self):
        r = Rectangle(2, 3, 0, 1)
        self.assertEqual((r.x1, r.y1, r.x2, r.y2), (F(0), F(1), F(2), F(3)))

    def test_degenerate_detection(self):
        self.assertTrue(Rectangle(1, 0, 1, 5).is_degenerate)
        self.assertTrue(Rectangle(0, 2, 4, 2).is_degenerate)
        self.assertFalse(Rectangle(0, 0, 1, 1).is_degenerate)

    def test_float_rejected(self):
        with self.assertRaises(TypeError):
            Rectangle(0.5, 0, 1, 1)

    def test_string_and_fraction_coordinates(self):
        r = Rectangle("1/3", 0, Fraction(2, 3), 1)
        self.assertEqual(r.x2 - r.x1, F(1, 3))


class TestAreaPerimeter(unittest.TestCase):
    def test_empty(self):
        result = union([])
        self.assertEqual(result.area, 0)
        self.assertEqual(result.perimeter, 0)
        self.assertEqual(result.polygons, ())

    def test_single_rectangle(self):
        result = union([Rectangle(0, 0, 2, 3)])
        self.assertEqual(result.area, 6)
        self.assertEqual(result.perimeter, 10)

    def test_adjacent_unit_squares(self):
        # Two unit squares sharing an edge: area 2, perimeter 6.
        result = union([Rectangle(0, 0, 1, 1), Rectangle(1, 0, 2, 1)])
        self.assertEqual(result.area, 2)
        self.assertEqual(result.perimeter, 6)
        self.assertEqual(len(result.polygons), 1)
        self.assertEqual(
            result.polygons[0].outer, ring([(0, 0), (2, 0), (2, 1), (0, 1)])
        )

    def test_corner_touching_squares(self):
        # Corner contact: two separate components, no merged figure-eight.
        result = union([Rectangle(0, 0, 1, 1), Rectangle(1, 1, 2, 2)])
        self.assertEqual(result.area, 2)
        self.assertEqual(result.perimeter, 8)
        self.assertEqual(len(result.polygons), 2)
        self.assertEqual(
            result.polygons[0].outer, ring([(0, 0), (1, 0), (1, 1), (0, 1)])
        )
        self.assertEqual(
            result.polygons[1].outer, ring([(1, 1), (2, 1), (2, 2), (1, 2)])
        )

    def test_containment(self):
        result = union([Rectangle(0, 0, 4, 4), Rectangle(1, 1, 2, 2)])
        self.assertEqual(result.area, 16)
        self.assertEqual(result.perimeter, 16)
        self.assertEqual(len(result.polygons), 1)
        self.assertEqual(result.polygons[0].holes, ())

    def test_partial_overlap(self):
        result = union([Rectangle(0, 0, 2, 2), Rectangle(1, 1, 3, 3)])
        self.assertEqual(result.area, 7)
        self.assertEqual(result.perimeter, 12)
        self.assertEqual(len(result.polygons), 1)

    def test_duplicate_rectangles(self):
        result = union([Rectangle(0, 0, 1, 1)] * 3)
        self.assertEqual(result.area, 1)
        self.assertEqual(result.perimeter, 4)

    def test_zero_area_rectangles_ignored(self):
        only_degenerate = union([Rectangle(0, 0, 1, 0), Rectangle(2, 2, 2, 2)])
        self.assertEqual(only_degenerate.area, 0)
        self.assertEqual(only_degenerate.perimeter, 0)
        self.assertEqual(only_degenerate.polygons, ())

        with_line = union([Rectangle(0, 0, 1, 1), Rectangle(0, 0, 1, 0)])
        without_line = union([Rectangle(0, 0, 1, 1)])
        self.assertEqual(with_line, without_line)

    def test_rational_coordinates(self):
        half = Rectangle(0, 0, F(1, 2), F(1, 2))
        result = union([half])
        self.assertEqual(result.area, F(1, 4))
        self.assertEqual(result.perimeter, 2)

        overlap = union(
            [Rectangle(0, 0, 1, 1), Rectangle(F(1, 2), F(1, 2), F(3, 2), F(3, 2))]
        )
        self.assertEqual(overlap.area, F(7, 4))
        self.assertEqual(overlap.perimeter, 6)

    def test_u_shape(self):
        result = union(
            [Rectangle(0, 0, 1, 3), Rectangle(2, 0, 3, 3), Rectangle(0, 0, 3, 1)]
        )
        self.assertEqual(result.area, 7)
        self.assertEqual(result.perimeter, 16)
        self.assertEqual(len(result.polygons), 1)
        self.assertEqual(result.polygons[0].holes, ())


class TestContours(unittest.TestCase):
    def test_outer_ring_is_ccw_and_canonical(self):
        (polygon,) = union_contours([Rectangle(1, 1, 3, 4)])
        self.assertGreater(signed_area(polygon.outer), 0)
        self.assertEqual(
            polygon.outer, ring([(1, 1), (3, 1), (3, 4), (1, 4)])
        )

    def test_hole_is_cw(self):
        frame = [
            Rectangle(0, 0, 3, 1),
            Rectangle(0, 2, 3, 3),
            Rectangle(0, 1, 1, 2),
            Rectangle(2, 1, 3, 2),
        ]
        result = union(frame)
        self.assertEqual(result.area, 8)
        self.assertEqual(result.perimeter, 16)
        (polygon,) = result.polygons
        self.assertEqual(polygon.outer, ring([(0, 0), (3, 0), (3, 3), (0, 3)]))
        (hole,) = polygon.holes
        self.assertLess(signed_area(hole), 0)
        self.assertEqual(hole, ring([(1, 1), (1, 2), (2, 2), (2, 1)]))
        self.assertEqual(polygon.area, 8)
        self.assertEqual(polygon.perimeter, 16)

    def test_pinched_hole_shares_one_vertex_with_outer(self):
        # A ring of cells whose inner hole touches the outer boundary at
        # exactly one point (diagonal pinch at (1, 2)).
        cells = [
            (0, 0), (1, 0), (2, 0),
            (0, 1),         (2, 1),
                    (1, 2), (2, 2),
        ]
        result = union([Rectangle(x, y, x + 1, y + 1) for x, y in cells])
        self.assertEqual(result.area, 7)
        self.assertEqual(result.perimeter, 16)
        (polygon,) = result.polygons
        (hole,) = polygon.holes
        self.assertEqual(hole, ring([(1, 1), (1, 2), (2, 2), (2, 1)]))
        self.assertIn((F(1), F(2)), polygon.outer)
        self.assertIn((F(1), F(2)), hole)
        assert_simple_ring(self, polygon.outer)
        assert_simple_ring(self, hole)

    def test_corner_touch_rings_are_simple(self):
        result = union([Rectangle(0, 0, 1, 1), Rectangle(1, 1, 2, 2)])
        for polygon in result.polygons:
            assert_simple_ring(self, polygon.outer)

    def test_convenience_functions(self):
        rects = [Rectangle(0, 0, 1, 1), Rectangle(1, 0, 2, 1)]
        self.assertEqual(union_area(rects), 2)
        self.assertEqual(union_perimeter(rects), 6)
        self.assertEqual(len(union_contours(rects)), 1)

    def test_raw_tuples_accepted(self):
        result = union([(0, 0, 1, 1), (1, 0, 2, 1)])
        self.assertEqual(result.area, 2)


class TestRandomizedConsistency(unittest.TestCase):
    def test_against_brute_force_and_internal_consistency(self):
        rng = random.Random(20260924)
        for _ in range(300):
            rects = []
            for _ in range(rng.randint(1, 6)):
                x1 = F(rng.randint(-8, 8), rng.choice([1, 1, 2, 4]))
                y1 = F(rng.randint(-8, 8), rng.choice([1, 1, 2, 4]))
                x2 = x1 + F(rng.randint(0, 6), rng.choice([1, 2]))
                y2 = y1 + F(rng.randint(0, 6), rng.choice([1, 2]))
                rects.append(Rectangle(x1, y1, x2, y2))
            result = union(rects)

            # Area agrees with an independent slab-decomposition algorithm.
            self.assertEqual(result.area, brute_force_area(rects))
            self.assertEqual(result.area, sum(p.area for p in result.polygons))
            self.assertEqual(
                result.perimeter, sum(p.perimeter for p in result.polygons)
            )

            for polygon in result.polygons:
                self.assertGreater(signed_area(polygon.outer), 0)
                assert_simple_ring(self, polygon.outer)
                for hole in polygon.holes:
                    self.assertLess(signed_area(hole), 0)
                    assert_simple_ring(self, hole)


if __name__ == "__main__":
    unittest.main()
