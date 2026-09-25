"""Tiled canvas, layers and compositing.

Every layer is filled by the same exact scanline pipeline (the unified
coverage rule); per-layer clip rects and boolean masks are applied as
per-pixel AND filters, and layers are union-composited (boolean OR), so
coverage is never double counted and shared edges cannot crack.

Edits return the exact set of dirty tiles (derived from the exact
union of the old and new geometry bboxes) and only those tiles are
re-filled; ``canvas.stats['rows']`` records how many scanline rows were
actually recomputed.
"""

from math import ceil, floor
from fractions import Fraction

from .geometry import NONZERO, Transform, norm_rings, rings_bbox
from .raster import fill_rows

_HALF = Fraction(1, 2)


class Layer:
    """A fill layer: rings + fill rule + optional transform/mask/clip.

    * ``rings``: list of rings in local coordinates (ints/Fractions).
    * ``rule``: 'nonzero' or 'evenodd'.
    * ``transform``: exact affine Transform (singular => empty layer).
    * ``mask``: callable(x, y) -> bool over integer pixel coords, or None.
    * ``clip``: (x, y, w, h) pixel rect, or None.
    """

    def __init__(self, rings, rule=NONZERO, transform=None, mask=None,
                 clip=None, name=None):
        self.rings = norm_rings(rings)
        self.rule = rule
        self.transform = transform if transform is not None else Transform()
        self.mask = mask
        self.clip = clip
        self.name = name or "layer"

    def world_rings(self):
        return self.transform.apply_rings(self.rings)

    def world_bbox(self):
        if self.transform.is_singular:
            return None
        return rings_bbox(self.world_rings())


class TiledCanvas:
    def __init__(self, width, height, tile_size=8):
        self.width = width
        self.height = height
        self.tile_size = tile_size
        self.layers = []
        self.tiles = {}
        self.stats = {"rows": 0, "tiles": 0}

    def add_layer(self, layer):
        self.layers.append(layer)
        return len(self.layers) - 1

    def _tiles_for_bbox(self, bbox):
        """Tiles whose pixel centers can be covered by ``bbox``.

        Pixel i is a candidate iff minx <= i + 1/2 <= maxx, computed
        exactly with floor/ceil -- no epsilon margins.
        """
        if bbox is None:
            return set()
        minx, miny, maxx, maxy = bbox
        i_lo = max(ceil(minx - _HALF), 0)
        i_hi = min(floor(maxx - _HALF), self.width - 1)
        j_lo = max(ceil(miny - _HALF), 0)
        j_hi = min(floor(maxy - _HALF), self.height - 1)
        if i_hi < i_lo or j_hi < j_lo:
            return set()
        ts = self.tile_size
        return {(tx, ty)
                for tx in range(i_lo // ts, i_hi // ts + 1)
                for ty in range(j_lo // ts, j_hi // ts + 1)}

    def _tiles_for_bboxes(self, *bboxes):
        dirty = set()
        for bbox in bboxes:
            dirty |= self._tiles_for_bbox(bbox)
        return dirty

    def render_tile(self, tx, ty):
        ts = self.tile_size
        x0, y0 = tx * ts, ty * ts
        tw = min(ts, self.width - x0)
        th = min(ts, self.height - y0)
        cov = [[False] * tw for _ in range(th)]
        for layer in self.layers:
            if layer.transform.is_singular:
                continue  # defined: singular transform => no coverage
            rings = layer.world_rings()
            filled = fill_rows(rings, layer.rule, x0, y0, tw, th,
                               counter=self.stats)
            for jj in range(th):
                for ii in range(tw):
                    if not filled[jj][ii]:
                        continue
                    gx, gy = x0 + ii, y0 + jj
                    if layer.clip is not None:
                        cx, cy, cw, ch = layer.clip
                        if not (cx <= gx < cx + cw and cy <= gy < cy + ch):
                            continue
                    if layer.mask is not None and not layer.mask(gx, gy):
                        continue
                    cov[jj][ii] = True
        self.tiles[(tx, ty)] = cov
        self.stats["tiles"] += 1
        return cov

    def render_all(self):
        ts = self.tile_size
        for ty in range(0, (self.height + ts - 1) // ts):
            for tx in range(0, (self.width + ts - 1) // ts):
                self.render_tile(tx, ty)

    def move_vertex(self, layer_index, ring_index, vertex_index, new_point):
        """Move one vertex; re-fill only the dirty tiles and return them."""
        layer = self.layers[layer_index]
        old_bbox = layer.world_bbox()
        layer.rings[ring_index][vertex_index] = (
            Fraction(new_point[0]), Fraction(new_point[1]))
        new_bbox = layer.world_bbox()
        dirty = self._tiles_for_bboxes(old_bbox, new_bbox)
        for tx, ty in sorted(dirty):
            self.render_tile(tx, ty)
        return dirty

    def coverage(self):
        """Full-canvas bool grid assembled from cached tiles."""
        grid = [[False] * self.width for _ in range(self.height)]
        ts = self.tile_size
        for (tx, ty), tile in self.tiles.items():
            for jj, row in enumerate(tile):
                y = ty * ts + jj
                for ii, v in enumerate(row):
                    x = tx * ts + ii
                    if 0 <= x < self.width and 0 <= y < self.height:
                        grid[y][x] = grid[y][x] or v
        return grid

    def reset_stats(self):
        self.stats = {"rows": 0, "tiles": 0}
