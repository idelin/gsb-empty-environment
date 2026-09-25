"""Runnable unittest suite for the exact scanline fill engine.

Run with:  python3.11 -m unittest discover -s tests -v
"""

import random
import unittest
from fractions import Fraction

from scanfill import (
    EVENODD,
    NONZERO,
    Layer,
    TiledCanvas,
    Transform,
    fill_rows,
    point_in_rings,
)


def reference_coverage(layers, width, height):
    """Per-pixel oracle: unified rule = per-layer fill AND clip AND mask,
    layers union-composited (OR)."""
    grid = [[False] * width for _ in range(height)]
    for y in range(height):
        for x in range(width):
            cx = Fraction(2 * x + 1, 2)
            cy = Fraction(2 * y + 1, 2)
            for layer in layers:
                if layer.transform.is_singular:
                    continue
                if layer.clip is not None:
                    rx, ry, rw, rh = layer.clip
                    if not (rx <= x < rx + rw and ry <= y < ry + rh):
                        continue
                if layer.mask is not None and not layer.mask(x, y):
                    continue
                if point_in_rings(cx, cy, layer.world_rings(), layer.rule):
                    grid[y][x] = True
                    break
    return grid


def fresh_render(layers, width, height, tile_size=4):
    canvas = TiledCanvas(width, height, tile_size)
    for layer in layers:
        canvas.add_layer(layer)
    canvas.render_all()
    return canvas


def covered(grid):
    return {(x, y) for y, row in enumerate(grid)
            for x, v in enumerate(row) if v}


class SharedEdgeTests(unittest.TestCase):
    """Two polygons sharing an edge: OR of separate fills == fill of union."""

    def test_shared_vertical_edge_rectangles(self):
        a = Layer([[(0, 0), (4, 0), (4, 4), (0, 4)]])
        b = Layer([[(4, 0), (8, 0), (8, 4), (4, 4)]])
        union = Layer([[(0, 0), (8, 0), (8, 4), (0, 4)]])
        ab = fresh_render([a, b], 8, 4).coverage()
        u = fresh_render([union], 8, 4).coverage()
        self.assertEqual(ab, u)
        self.assertEqual(len(covered(ab)), 32)  # full 8x4, no cracks

    def test_shared_slanted_edge_triangles(self):
        a = Layer([[(0, 0), (6, 0), (3, 6)]])
        b = Layer([[(6, 0), (6, 6), (3, 6)]])
        union = Layer([[(0, 0), (6, 0), (6, 6), (3, 6)]])
        for rule in (NONZERO, EVENODD):
            a.rule = b.rule = union.rule = rule
            ab = fresh_render([a, b], 6, 6).coverage()
            u = fresh_render([union], 6, 6).coverage()
            self.assertEqual(ab, u, msg=f"rule={rule}")

    def test_shared_edge_rational_coords(self):
        h = Fraction(7, 2)  # shared edge passes through pixel centers
        a = Layer([[(0, 0), (h, 0), (h, 4), (0, 4)]])
        b = Layer([[(h, 0), (7, 0), (7, 4), (h, 4)]])
        union = Layer([[(0, 0), (7, 0), (7, 4), (0, 4)]])
        ab = fresh_render([a, b], 7, 4).coverage()
        u = fresh_render([union], 7, 4).coverage()
        self.assertEqual(ab, u)
        # Boundary column x=3 (center 7/2) covered; boolean union means
        # no crack and no double-counted coverage.
        self.assertIn((3, 1), covered(ab))


class HoleOrientationTests(unittest.TestCase):
    OUTER = [(0, 0), (10, 0), (10, 10), (0, 10)]       # CCW
    HOLE_CCW = [(2, 2), (6, 2), (6, 6), (2, 6)]        # same direction
    HOLE_CW = [(2, 2), (2, 6), (6, 6), (6, 2)]         # reversed

    def test_nonzero_rule(self):
        same = fresh_render([Layer([self.OUTER, self.HOLE_CCW], NONZERO)],
                            10, 10).coverage()
        rev = fresh_render([Layer([self.OUTER, self.HOLE_CW], NONZERO)],
                           10, 10).coverage()
        # Same-direction hole: winding 2 inside -> still covered.
        self.assertIn((3, 3), covered(same))
        # Reversed hole: winding 0 inside -> punched out.
        hole_px = {(x, y) for x in range(2, 6) for y in range(2, 6)}
        self.assertTrue(hole_px.isdisjoint(covered(rev)))
        # Outside the hole both agree; same == rev plus the hole pixels.
        self.assertEqual(covered(same) - hole_px, covered(rev) - hole_px)
        self.assertEqual(covered(same), covered(rev) | hole_px)

    def test_evenodd_rule(self):
        same = fresh_render([Layer([self.OUTER, self.HOLE_CCW], EVENODD)],
                            10, 10).coverage()
        rev = fresh_render([Layer([self.OUTER, self.HOLE_CW], EVENODD)],
                           10, 10).coverage()
        hole_px = {(x, y) for x in range(2, 6) for y in range(2, 6)}
        # Even-odd punches the hole regardless of orientation.
        self.assertTrue(hole_px.isdisjoint(covered(same)))
        self.assertTrue(hole_px.isdisjoint(covered(rev)))
        self.assertEqual(same, rev)


class VertexMoveDirtyTileTests(unittest.TestCase):
    def test_move_vertex_updates_only_dirty_tiles(self):
        width = height = 16
        ts = 4
        canvas = TiledCanvas(width, height, ts)
        canvas.add_layer(Layer([[(1, 1), (6, 1), (2, 5)]], NONZERO))
        canvas.render_all()
        before = {t: [row[:] for row in rows]
                  for t, rows in canvas.tiles.items()}
        before_cov = canvas.coverage()

        canvas.reset_stats()
        dirty = canvas.move_vertex(0, 0, 2, (4, 5))
        rows_recomputed = canvas.stats["rows"]

        # Measure a full re-render for comparison.
        probe = TiledCanvas(width, height, ts)
        probe.add_layer(Layer([[(1, 1), (6, 1), (4, 5)]], NONZERO))
        probe.render_all()
        full_rows = probe.stats["rows"]

        # Dirty region covers old+new geometry: bbox x in [1,6], y in
        # [1,5] -> tiles (0..1, 0..1).
        self.assertEqual(dirty, {(0, 0), (1, 0), (0, 1), (1, 1)})
        # Partial recompute: 4 tiles * 4 rows = 16 rows, not 64 (full).
        self.assertEqual(rows_recomputed, 16)
        self.assertEqual(full_rows, 64)
        self.assertLess(rows_recomputed, full_rows)
        print(f"\n[record] vertex move: dirty_tiles={len(dirty)}, "
              f"rows_recomputed={rows_recomputed}, full_render_rows={full_rows}")

        # Tiles outside the dirty set are untouched.
        for t, rows in canvas.tiles.items():
            if t not in dirty:
                self.assertEqual(rows, before[t], msg=f"tile {t} changed")

        # Old coverage removed, new coverage added, result == full re-render.
        fresh = fresh_render([Layer([[(1, 1), (6, 1), (4, 5)]], NONZERO)],
                             width, height, ts)
        after_cov = canvas.coverage()
        self.assertEqual(after_cov, fresh.coverage())
        old_only = covered(before_cov) - covered(after_cov)
        new_only = covered(after_cov) - covered(before_cov)
        self.assertTrue(old_only, "expected vacated pixels")
        self.assertTrue(new_only, "expected newly covered pixels")
        # And the per-pixel oracle agrees everywhere.
        self.assertEqual(after_cov,
                         reference_coverage(canvas.layers, width, height))


class NegativeAndDegenerateTests(unittest.TestCase):
    def test_negative_coordinates(self):
        poly = Layer([[(-3, -3), (5, -3), (5, 5), (-3, 5)]])
        canvas = fresh_render([poly], 8, 8)
        self.assertEqual(canvas.coverage(),
                         reference_coverage([poly], 8, 8))
        self.assertEqual(len(covered(canvas.coverage())), 25)  # 0..4 x 0..4

    def test_singular_transform_is_defined_empty(self):
        rect = [[(1, 1), (5, 1), (5, 5), (1, 5)]]
        singular = Transform.scale(0, 1)  # determinant exactly 0
        self.assertTrue(singular.is_singular)
        layer = Layer(rect, transform=singular)
        canvas = fresh_render([layer], 8, 8)
        self.assertEqual(covered(canvas.coverage()), set())
        # Moving a vertex under a singular transform is a defined no-op too.
        canvas.reset_stats()
        dirty = canvas.move_vertex(0, 0, 0, (2, 2))
        self.assertEqual(dirty, set())
        self.assertEqual(canvas.stats["rows"], 0)

    def test_nonsingular_exact_transform(self):
        # Shear by 1/2 and translate by (-1/2, 1): all-exact rationals.
        t = Transform(a=1, b=Fraction(1, 2), c=0, d=1,
                      tx=Fraction(-1, 2), ty=1)
        layer = Layer([[(0, 0), (4, 0), (4, 4), (0, 4)]], transform=t)
        canvas = fresh_render([layer], 8, 8)
        self.assertEqual(canvas.coverage(),
                         reference_coverage([layer], 8, 8))

    def test_degenerate_geometry(self):
        cases = [
            [],                              # no rings
            [[]],                            # empty ring
            [[(3, 3)]],                      # single point
            [[(0, 0), (2, 2)]],              # 2-point ring (collapsed)
            [[(1, 1), (1, 1), (5, 1), (5, 5), (1, 5)]],  # zero-length edge
            [[(1, 1), (3, 1), (5, 1), (5, 5), (1, 5)]],  # collinear vertex
            [[(1, 1), (5, 1), (5, 1), (5, 5), (1, 5), (1, 5)]],
        ]
        for rings in cases:
            for rule in (NONZERO, EVENODD):
                layer = Layer(rings, rule)
                canvas = fresh_render([layer], 8, 8)
                self.assertEqual(
                    canvas.coverage(),
                    reference_coverage([layer], 8, 8),
                    msg=f"rings={rings} rule={rule}")


class RationalBoundaryTests(unittest.TestCase):
    def test_boundary_through_pixel_centers(self):
        # Hypotenuse x + y = 6 passes exactly through pixel centers.
        tri = [[(Fraction(1, 2), Fraction(1, 2)),
                (Fraction(11, 2), Fraction(1, 2)),
                (Fraction(1, 2), Fraction(11, 2))]]
        for rule in (NONZERO, EVENODD):
            layer = Layer(tri, rule)
            canvas = fresh_render([layer], 8, 8)
            cov = canvas.coverage()
            self.assertEqual(cov, reference_coverage([layer], 8, 8))
            # Pixel centers exactly on the hypotenuse are covered (closed
            # set), e.g. (2,3): center (5/2, 7/2) sums to 6.
            self.assertIn((2, 3), covered(cov))
            self.assertIn((4, 1), covered(cov))

    def test_horizontal_edge_on_sample_line(self):
        # Horizontal edge at y = 5/2 lies exactly on scanline of row 2.
        poly = [[(1, Fraction(5, 2)), (6, Fraction(5, 2)), (6, 6), (1, 6)]]
        layer = Layer(poly, NONZERO)
        canvas = fresh_render([layer], 8, 8)
        cov = canvas.coverage()
        self.assertEqual(cov, reference_coverage([layer], 8, 8))
        for x in range(1, 6):
            self.assertIn((x, 2), covered(cov))  # boundary row covered


class LayerCompositingTests(unittest.TestCase):
    def test_clip_and_mask_unified_compositing(self):
        base = Layer([[(0, 0), (8, 0), (8, 8), (0, 8)]], NONZERO,
                     clip=(2, 2, 6, 6), name="clipped")
        masked = Layer([[(4, 4), (12, 4), (12, 12), (4, 12)]], NONZERO,
                       mask=lambda x, y: (x + y) % 2 == 0, name="masked")
        holes = Layer([[(1, 9), (7, 9), (7, 15), (1, 15)],
                       [(3, 11), (5, 11), (5, 13), (3, 13)]], EVENODD,
                      name="holed")
        layers = [base, masked, holes]
        canvas = fresh_render(layers, 16, 16)
        self.assertEqual(canvas.coverage(),
                         reference_coverage(layers, 16, 16))
        cov = covered(canvas.coverage())
        # Clip window of base layer is respected.
        self.assertNotIn((1, 1), cov)
        self.assertIn((2, 2), cov)
        # Mask drops odd checkerboard pixels of the masked layer only.
        self.assertIn((11, 11), cov)       # 11+11 even -> kept
        self.assertNotIn((10, 11), cov)    # 10+11 odd  -> masked out


class RandomizedOracleTests(unittest.TestCase):
    def test_random_polygons_match_per_pixel_oracle(self):
        rng = random.Random(20260925)
        for trial in range(30):
            n = rng.randint(3, 9)
            ring = [(Fraction(rng.randint(-4, 12), rng.choice((1, 2, 4))),
                     Fraction(rng.randint(-4, 12), rng.choice((1, 2, 4))))
                    for _ in range(n)]
            rule = rng.choice((NONZERO, EVENODD))
            layer = Layer([ring], rule)
            canvas = fresh_render([layer], 8, 8)
            self.assertEqual(
                canvas.coverage(),
                reference_coverage([layer], 8, 8),
                msg=f"trial={trial} ring={ring} rule={rule}")


if __name__ == "__main__":
    unittest.main()
