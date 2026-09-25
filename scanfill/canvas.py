"""Tiled coverage canvas with dirty-region tracked editing.

The canvas owns a set of world-space polygons and maintains the exact
0/1 coverage (union of all polygons, each under its own fill rule) one
tile at a time.  Editing operations return the set of dirty tiles and
re-fill only those; ``stats`` records how much work was actually done
so callers can verify there is no whole-canvas fallback.
"""
from __future__ import annotations

import math
from fractions import Fraction
from typing import Dict, FrozenSet, Optional, Tuple

from .geometry import Point, Transform, point, to_frac
from .polygon import Polygon
from .scanline import fill_row_spans

_HALF = Fraction(1, 2)

Tile = Tuple[int, int]


class Canvas:
    """A width x height grid of 0/1 coverage, refilled per tile.

    Pixel (px, py) covers the world square
        [ox + px, ox + px + 1] x [oy + py, oy + py + 1]
    and is sampled at its centre, so negative world coordinates are
    supported simply by choosing a negative origin.
    """

    def __init__(self, width: int, height: int, tile: int = 8, origin=(0, 0)):
        if width <= 0 or height <= 0 or tile <= 0:
            raise ValueError("width, height and tile must be positive")
        self.width = width
        self.height = height
        self.tile = tile
        self.ox = to_frac(origin[0])
        self.oy = to_frac(origin[1])
        self.coverage = bytearray(width * height)
        self._polys: Dict[object, Polygon] = {}
        self.stats = {
            "rows_recomputed": 0,   # distinct pixel rows re-evaluated
            "row_passes": 0,        # tile-row passes (>= rows_recomputed)
            "tiles_recomputed": 0,
        }

    # ---------------------------------------------------------- edits
    def add_polygon(self, pid, polygon: Polygon,
                    transform: Optional[Transform] = None) -> FrozenSet[Tile]:
        poly = polygon.transformed(transform) if transform is not None else polygon
        self._polys[pid] = poly
        dirty = self._tiles_for(poly.bounds())
        self._refill(dirty)
        return dirty

    def move_vertex(self, pid, ring: int, index: int, new_xy) -> FrozenSet[Tile]:
        """Move one vertex; re-fills tiles under the old and new shape."""
        old = self._polys[pid]
        rings = [list(r) for r in old.rings]
        rings[ring][index] = point(*new_xy)
        new = Polygon(rings, old.fill_rule)
        self._polys[pid] = new
        dirty = self._tiles_for(old.bounds()) | self._tiles_for(new.bounds())
        self._refill(dirty)
        return dirty

    def remove_polygon(self, pid) -> FrozenSet[Tile]:
        old = self._polys.pop(pid)
        dirty = self._tiles_for(old.bounds())
        self._refill(dirty)
        return dirty

    # --------------------------------------------------------- queries
    def pixel(self, px: int, py: int) -> int:
        return self.coverage[py * self.width + px]

    def count(self) -> int:
        return sum(self.coverage)

    def rows_as_text(self):
        return [
            "".join("#" if self.coverage[py * self.width + px] else "."
                    for px in range(self.width))
            for py in range(self.height)
        ]

    # -------------------------------------------------------- internals
    def _tiles_for(self, bounds) -> FrozenSet[Tile]:
        """Tiles whose pixel centres can lie inside the given bounds."""
        if bounds is None:
            return frozenset()
        minx, miny, maxx, maxy = bounds
        px0 = max(0, math.floor(minx - self.ox))
        py0 = max(0, math.floor(miny - self.oy))
        px1 = min(self.width, math.ceil(maxx - self.ox))
        py1 = min(self.height, math.ceil(maxy - self.oy))
        if px0 >= px1 or py0 >= py1:
            return frozenset()
        t = self.tile
        return frozenset(
            (tx, ty)
            for tx in range(px0 // t, (px1 - 1) // t + 1)
            for ty in range(py0 // t, (py1 - 1) // t + 1)
        )

    def _refill(self, tiles) -> None:
        rows_seen = set()
        row_passes = 0
        for tx, ty in sorted(tiles):
            x0 = tx * self.tile
            y0 = ty * self.tile
            x1 = min(x0 + self.tile, self.width)
            y1 = min(y0 + self.tile, self.height)
            relevant = [
                poly for poly in self._polys.values()
                if self._overlaps_tile(poly.bounds(), x0, y0, x1, y1)
            ]
            for py in range(y0, y1):
                yc = self.oy + py + _HALF
                row = bytearray(x1 - x0)
                for poly in relevant:
                    for s, e in fill_row_spans(
                            poly.rings, yc, x0, x1, poly.fill_rule, self.ox):
                        row[s - x0:e - x0] = b"\x01" * (e - s)
                self.coverage[py * self.width + x0: py * self.width + x1] = row
                rows_seen.add(py)
                row_passes += 1
        self.stats["rows_recomputed"] = len(rows_seen)
        self.stats["row_passes"] = row_passes
        self.stats["tiles_recomputed"] = len(tiles)

    def _overlaps_tile(self, bounds, x0, y0, x1, y1) -> bool:
        if bounds is None:
            return False
        minx, miny, maxx, maxy = bounds
        return not (maxx <= self.ox + x0 or minx >= self.ox + x1 or
                    maxy <= self.oy + y0 or miny >= self.oy + y1)
