"""Runnable tests for the exact scanline fill package.

Run with:  python3.11 -m unittest discover -s tests -v
"""
import unittest
from fractions import Fraction

from scanfill import (
    EVEN_ODD, NONZERO, Canvas, Layer, Mask, Polygon, Transform, composite,
    winding_at,
)

HALF = Fraction(1, 2)


def reference_cover(polys, width, height, origin=(0, 0)):
    """Pixel-by-pixel reference: brute-force point test per pixel centre."""
    ox, oy = Fraction(origin[0]), Fraction(origin[1])
    out = bytearray(width * height)
    for py in range(height):
        for px in range(width):
            wx = ox + px + HALF
            wy = oy + py + HALF
            if any(winding_at(p.rings, wx, wy, p.fill_rule) for p in polys):
                out[py * width + px] = 1
    return out


def render(polys, width, height, origin=(0, 0), tile=4):
    canvas = Canvas(width, height, tile=tile, origin=origin)
    for i, poly in enumerate(polys):
        canvas.add_polygon(i, poly)
    return canvas


class SharedEdgeUnionTest(unittest.TestCase):
    """Two polygons sharing an edge: combined fill == union fill."""

    def check_pair(self, rings_a, rings_b, rings_union, width, height):
        for rule in (EVEN_ODD, NONZERO):
            with self.subTest(rule=rule):
                pa = Polygon(rings_a, rule)
                pb = Polygon(rings_b, rule)
                pu = Polygon(rings_union, rule)
                ca = render([pa], width, height)
                cb = render([pb], width, height)
                cu = render([pu], width, height)
                combined = bytearray(width * height)
                overlap = 0
                for i in range(width * height):
                    a, b = ca.coverage[i], cb.coverage[i]
                    if a and b:
                        overlap += 1
                    combined[i] = 1 if (a or b) else 0
                self.assertEqual(overlap, 0, "double-counted coverage")
                self.assertEqual(bytes(combined), bytes(cu.coverage),
                                 "crack or mismatch along the shared edge")

    def test_rectangles_sharing_vertical_edge(self):
        a = [[(0, 0), (4, 0), (4, 4), (0, 4)]]
        b = [[(4, 0), (8, 0), (8, 4), (4, 4)]]
        u = [[(0, 0), (8, 0), (8, 4), (0, 4)]]
        self.check_pair(a, b, u, 10, 6)

    def test_triangles_sharing_diagonal_through_pixel_centres(self):
        # The shared edge x + y = 4 passes exactly through the pixel
        # centres (0.5, 3.5), (1.5, 2.5), (2.5, 1.5), (3.5, 0.5).
        a = [[(0, 0), (4, 0), (0, 4)]]
        b = [[(4, 0), (4, 4), (0, 4)]]
        u = [[(0, 0), (4, 0), (4, 4), (0, 4)]]
        self.check_pair(a, b, u, 6, 6)

    def test_rectangles_sharing_rational_edge(self):
        # Shared edge x = 5/2 passes exactly through pixel centres.
        h = (5, 2)
        a = [[(0, 0), (h, 0), (h, 5), (0, 5)]]
        b = [[(h, 0), (6, 0), (6, 5), (h, 5)]]
        u = [[(0, 0), (6, 0), (6, 5), (0, 5)]]
        self.check_pair(a, b, u, 8, 7)


class HoleOrientationTest(unittest.TestCase):
    """Hole ring reversed vs same orientation, under both fill rules."""

    OUTER = [(0, 0), (8, 0), (8, 8), (0, 8)]
    HOLE_SAME = [(2, 2), (6, 2), (6, 6), (2, 6)]        # same winding as OUTER
    HOLE_REVERSED = [(2, 2), (2, 6), (6, 6), (6, 2)]    # opposite winding

    def fill_count(self, rings, rule):
        canvas = render([Polygon(rings, rule)], 10, 10, origin=(-1, -1))
        return canvas

    def test_nonzero(self):
        same = self.fill_count([self.OUTER, self.HOLE_SAME], NONZERO)
        rev = self.fill_count([self.OUTER, self.HOLE_REVERSED], NONZERO)
        # Same orientation: winding in the hole is +-2 -> still filled.
        self.assertEqual(same.count(), 64)
        self.assertEqual(same.pixel(4, 4 + 1), 1)  # pixel centre (4.5, 4.5)
        # Reversed: winding cancels to 0 -> hole is empty.
        self.assertEqual(rev.count(), 64 - 16)
        self.assertEqual(rev.pixel(5, 5), 0)
        # Both agree outside the hole.
        for py in range(10):
            for px in range(10):
                in_hole = 3 <= px <= 6 and 3 <= py <= 6
                if not in_hole:
                    self.assertEqual(same.pixel(px, py), rev.pixel(px, py))

    def test_evenodd(self):
        same = self.fill_count([self.OUTER, self.HOLE_SAME], EVEN_ODD)
        rev = self.fill_count([self.OUTER, self.HOLE_REVERSED], EVEN_ODD)
        # Orientation is irrelevant for even-odd: hole empty either way.
        self.assertEqual(same.count(), 64 - 16)
        self.assertEqual(rev.count(), 64 - 16)
        self.assertEqual(bytes(same.coverage), bytes(rev.coverage))
        self.assertEqual(same.pixel(5, 5), 0)


class MoveVertexPartialRefillTest(unittest.TestCase):
    """Moving a vertex must update old+new coverage and keep other tiles."""

    def test_move_vertex(self):
        width = height = 32
        tile = 8
        canvas = Canvas(width, height, tile=tile)
        canvas.add_polygon("sq", Polygon(
            [[(4, 4), (12, 4), (12, 12), (4, 12)]], NONZERO))
        before = bytes(canvas.coverage)

        dirty = canvas.move_vertex("sq", 0, 2, (20, 20))
        after = bytes(canvas.coverage)

        # The dirty region must cover both the old and the new bounds.
        expected_dirty = frozenset(
            (tx, ty) for tx in range(3) for ty in range(3))
        self.assertEqual(dirty, expected_dirty)

        # Coverage matches a from-scratch render of the moved polygon.
        full = Canvas(width, height, tile=tile)
        full.add_polygon("sq", Polygon(
            [[(4, 4), (12, 4), (20, 20), (4, 12)]], NONZERO))
        self.assertEqual(after, bytes(full.coverage))

        # Tiles outside the dirty set are byte-identical to before.
        for py in range(height):
            for px in range(width):
                if (px // tile, py // tile) not in dirty:
                    self.assertEqual(before[py * width + px],
                                     after[py * width + px],
                                     f"tile ({px//tile},{py//tile}) changed")

        # Record and verify the actual amount of recomputed work:
        # it must be a strict partial refill, not a full-canvas fallback.
        rows = canvas.stats["rows_recomputed"]
        passes = canvas.stats["row_passes"]
        tiles = canvas.stats["tiles_recomputed"]
        print(f"\n[record] vertex move: recomputed {rows}/{height} distinct "
              f"rows, {passes} tile-row passes, {tiles} tiles")
        self.assertEqual(tiles, 9)
        self.assertEqual(rows, 24)
        self.assertLess(rows, height)


class GeometryEdgeCasesTest(unittest.TestCase):
    """Negative coordinates, transforms, singular and degenerate input."""

    def test_negative_coordinates(self):
        poly = Polygon([[(-6, -6), (-1, -6), (-1, -1), (-6, -1)]], NONZERO)
        canvas = render([poly], 8, 8, origin=(-8, -8))
        self.assertEqual(canvas.count(), 25)  # centres -5.5 .. -1.5, squared
        self.assertEqual(bytes(canvas.coverage),
                         bytes(reference_cover([poly], 8, 8, origin=(-8, -8))))

    def test_rational_scale_and_translate(self):
        base = Polygon([[(0, 0), (4, 0), (4, 4), (0, 4)]], NONZERO)
        t = Transform.scale((1, 2)).then(Transform.translate(1, 1))
        canvas = Canvas(6, 6, tile=2)
        canvas.add_polygon("p", base, t)
        expect = render([Polygon([[(1, 1), (3, 1), (3, 3), (1, 3)]], NONZERO)],
                        6, 6, tile=2)
        self.assertEqual(bytes(canvas.coverage), bytes(expect.coverage))
        self.assertEqual(canvas.count(), 4)

    def test_exact_rotation_90_into_negative_space(self):
        base = Polygon([[(1, 1), (3, 1), (3, 3), (1, 3)]], NONZERO)
        rot = Transform(0, -1, 0, 1, 0, 0)  # (x, y) -> (-y, x)
        canvas = Canvas(8, 8, tile=2, origin=(-4, -4))
        canvas.add_polygon("p", base, rot)
        # Rotated square covers world (-3, 1) .. (-1, 3): 4 pixel centres.
        self.assertEqual(canvas.count(), 4)
        self.assertEqual(canvas.pixel(1, 5), 1)   # centre (-2.5, 1.5)
        self.assertEqual(canvas.pixel(5, 1), 0)

    def test_singular_transform_fills_nothing(self):
        base = Polygon([[(1, 1), (5, 1), (5, 5), (1, 5)]], NONZERO)
        t = Transform.scale(0, 1)
        self.assertTrue(t.is_singular())
        self.assertEqual(t.determinant(), 0)
        for origin in ((0, 0), (-4, -4)):
            canvas = Canvas(8, 8, tile=4, origin=origin)
            dirty = canvas.add_polygon("p", base, t)
            self.assertEqual(canvas.count(), 0)
            self.assertEqual(bytes(canvas.coverage), bytes(64))
            self.assertIsInstance(dirty, frozenset)

    def test_degenerate_edges_are_defined(self):
        clean = Polygon([[(2, 2), (6, 2), (6, 6), (2, 6)]], NONZERO)
        messy = Polygon([[(2, 2), (2, 2), (6, 2), (6, 2), (6, 6),
                          (2, 6), (2, 6), (2, 2)]], NONZERO)
        spiky = Polygon([[(2, 2), (6, 2), (9, 2), (6, 2), (6, 6),
                          (6, 9), (6, 6), (2, 6)]], NONZERO)
        expected = render([clean], 12, 12)
        for rule in (EVEN_ODD, NONZERO):
            for rings in (messy.rings, spiky.rings):
                got = render([Polygon(rings, rule)], 12, 12)
                self.assertEqual(bytes(got.coverage), bytes(expected.coverage))
        self.assertEqual(expected.count(), 16)

    def test_empty_point_and_line_polygons(self):
        canvas = Canvas(8, 8, tile=4)
        canvas.add_polygon("empty", Polygon([[]], NONZERO))
        canvas.add_polygon("point", Polygon([[(3, 3)]] , NONZERO))
        canvas.add_polygon("line", Polygon([[(1, 1), (5, 5)]], NONZERO))
        canvas.add_polygon("flat", Polygon([[(1, 2), (5, 2), (3, 2)]], NONZERO))
        self.assertEqual(canvas.count(), 0)


class ReferenceParityTest(unittest.TestCase):
    """Scanline spans must match the per-pixel point-test reference."""

    def test_complex_shape_small_canvas(self):
        outer = [(0, 0), (11, 0), (11, 7), (5, 7), (5, 11), (0, 11)]  # concave L
        hole = [(2, 2), (2, 4), (4, 4), (4, 2)]                        # reversed
        # Island whose left edge x - y = -2 passes through pixel centres.
        island = [(6, 8), (10, 8), (8, 10)]
        # Rational vertices landing exactly on pixel centres.
        spike = [(7, 0), ((15, 2), (5, 2)), (9, 0)]
        for rule in (EVEN_ODD, NONZERO):
            with self.subTest(rule=rule):
                poly = Polygon([outer, hole, island, spike], rule)
                canvas = render([poly], 12, 12, tile=5)
                self.assertEqual(
                    bytes(canvas.coverage),
                    bytes(reference_cover([poly], 12, 12)),
                )

    def test_rational_vertices_on_pixel_centres(self):
        # Every vertex sits exactly on a pixel centre: the boundary
        # convention, not epsilon, decides those pixels.
        ring = [((1, 2), (1, 2)), ((11, 2), (1, 2)),
                ((11, 2), (11, 2)), ((1, 2), (11, 2))]
        for rule in (EVEN_ODD, NONZERO):
            poly = Polygon([ring], rule)
            canvas = render([poly], 7, 7, tile=3)
            self.assertEqual(bytes(canvas.coverage),
                             bytes(reference_cover([poly], 7, 7)))


class LayerCompositeTest(unittest.TestCase):
    """Layer clipping and boolean masks under the unified coverage rule."""

    def test_clip_and_boolean_mask(self):
        width = height = 12
        p1 = Polygon([[(1, 1), (9, 1), (9, 9), (1, 9)]], NONZERO)
        clip_poly = Polygon([[(4, 0), (12, 0), (12, 12), (4, 12)]], EVEN_ODD)
        p2 = Polygon([[(6, 4), (11, 4), (11, 11), (6, 11)]], NONZERO)
        cut = Polygon([[(7, 5), (7, 7), (9, 7), (9, 5)]], NONZERO)

        m1 = Mask.from_polygon(p1, width, height)
        mclip = Mask.from_polygon(clip_poly, width, height)
        m2 = Mask.from_polygon(p2, width, height) - Mask.from_polygon(cut, width, height)

        out = composite([Layer(m1, clip=mclip), Layer(m2)], width, height)

        for py in range(height):
            for px in range(width):
                wx, wy = px + HALF, py + HALF
                expected = (
                    (winding_at(p1.rings, wx, wy, NONZERO)
                     and winding_at(clip_poly.rings, wx, wy, EVEN_ODD))
                    or (winding_at(p2.rings, wx, wy, NONZERO)
                        and not winding_at(cut.rings, wx, wy, NONZERO))
                )
                self.assertEqual(out.get(px, py), int(expected),
                                 f"pixel ({px}, {py})")

    def test_shared_edge_layers_no_crack_no_double(self):
        # Two layers whose fills share the edge x = 4: composited union
        # equals the mask of the union polygon, and overlap is empty.
        width, height = 10, 6
        a = Polygon([[(0, 0), (4, 0), (4, 4), (0, 4)]], NONZERO)
        b = Polygon([[(4, 0), (8, 0), (8, 4), (4, 4)]], NONZERO)
        u = Polygon([[(0, 0), (8, 0), (8, 4), (0, 4)]], NONZERO)
        ma = Mask.from_polygon(a, width, height)
        mb = Mask.from_polygon(b, width, height)
        mu = Mask.from_polygon(u, width, height)
        self.assertEqual((ma & mb).count(), 0)
        out = composite([Layer(ma), Layer(mb)], width, height)
        self.assertEqual(bytes(out.data), bytes(mu.data))


if __name__ == "__main__":
    unittest.main()
