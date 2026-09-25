"""Multi-ring polygons with an explicit fill rule."""
from __future__ import annotations

from typing import Iterator, List, Optional, Tuple

from .geometry import Fraction, Point, Transform, point

EVEN_ODD = "evenodd"
NONZERO = "nonzero"
_RULES = (EVEN_ODD, NONZERO)


class Polygon:
    """A polygon built from one or more implicitly closed rings.

    The first ring is conventionally the outer boundary; further rings
    are holes or islands.  Ring orientation only matters for the
    NONZERO rule: a hole wound opposite to its outer ring cancels it
    (winding 0 -> empty), a hole wound the same way adds up (winding
    +-2 -> filled).  Under EVEN_ODD orientation is irrelevant.

    Degenerate input is *defined*, not patched over with epsilons:
    zero-length edges and horizontal edges never cross a scanline and
    therefore contribute nothing; spikes traversed out and back cancel
    exactly in both rules.
    """

    def __init__(self, rings, fill_rule: str = NONZERO):
        if fill_rule not in _RULES:
            raise ValueError(f"unknown fill rule {fill_rule!r}")
        self.fill_rule = fill_rule
        self.rings: List[List[Point]] = [
            [p if isinstance(p, Point) else point(*p) for p in ring]
            for ring in rings
        ]

    def transformed(self, t: Transform) -> "Polygon":
        return Polygon(
            [[t.apply(p) for p in ring] for ring in self.rings],
            self.fill_rule,
        )

    def edges(self) -> Iterator[Tuple[Point, Point]]:
        for ring in self.rings:
            n = len(ring)
            for i in range(n):
                yield ring[i], ring[(i + 1) % n]

    def bounds(self) -> Optional[Tuple[Fraction, Fraction, Fraction, Fraction]]:
        """Exact (minx, miny, maxx, maxy), or None for a point-less polygon."""
        xs = [p.x for ring in self.rings for p in ring]
        ys = [p.y for ring in self.rings for p in ring]
        if not xs:
            return None
        return (min(xs), min(ys), max(xs), max(ys))
