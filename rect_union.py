"""Exact area, perimeter and outer contours of a union of axis-aligned rectangles.

All arithmetic is exact, based on ``fractions.Fraction``.  No floating point
and no epsilon-based tolerances are used anywhere: every topological decision
(inside/outside, vertex pairing at touch points) follows from exact rational
comparison.

Conventions
-----------
* Coordinates are integers or rationals (``int``, ``Fraction`` or numeric
  strings such as ``"1/3"``).  ``float`` is rejected, because it would
  silently import binary rounding error into an exact computation.
* A rectangle is the closed set ``[x1, x2] x [y1, y2]``; argument order is
  normalized, so ``Rectangle(2, 3, 0, 1)`` is valid.
* Degenerate (zero-area) rectangles -- ``x1 == x2`` or ``y1 == y2`` -- are
  ignored: they contribute no area, no perimeter and no contour.
* Contours are simple closed rings: tuples of ``(Fraction, Fraction)``
  points, the closing point is *not* repeated.  Outer rings are
  counter-clockwise, holes are clockwise.  Every ring is canonicalized to
  start at its lexicographically smallest point, so output is deterministic.
* Shapes that touch only at a point (corner contact) yield separate rings
  that share that vertex; rings never self-intersect and never degenerate
  into figure-eights.
* Edge contact merges shapes into a single ring; collinear vertices along a
  straight boundary run are removed.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Iterable, Sequence, Tuple, Union

NumberLike = Union[int, Fraction, str]
Point = Tuple[Fraction, Fraction]
Ring = Tuple[Point, ...]

__all__ = [
    "Rectangle",
    "Polygon",
    "UnionResult",
    "union",
    "union_area",
    "union_perimeter",
    "union_contours",
    "signed_area",
]


def _to_fraction(value) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("boolean is not a valid coordinate")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        raise TypeError(
            "float coordinates are rejected: use int, Fraction or a string "
            "like '1/3' to keep the computation exact"
        )
    return Fraction(value)  # str and other Rational-likes


def _sign(v: Fraction) -> int:
    return (v > 0) - (v < 0)


class Rectangle:
    """Axis-aligned rectangle with exact rational coordinates."""

    __slots__ = ("x1", "y1", "x2", "y2")

    def __init__(self, x1, y1, x2, y2):
        x1, x2 = _to_fraction(x1), _to_fraction(x2)
        y1, y2 = _to_fraction(y1), _to_fraction(y2)
        if x2 < x1:
            x1, x2 = x2, x1
        if y2 < y1:
            y1, y2 = y2, y1
        self.x1 = x1
        self.y1 = y1
        self.x2 = x2
        self.y2 = y2

    @property
    def is_degenerate(self) -> bool:
        """Zero-area rectangles (a segment or a point) carry no 2D content."""
        return self.x1 == self.x2 or self.y1 == self.y2

    def __eq__(self, other):
        return (
            isinstance(other, Rectangle)
            and (self.x1, self.y1, self.x2, self.y2)
            == (other.x1, other.y1, other.x2, other.y2)
        )

    def __hash__(self):
        return hash((self.x1, self.y1, self.x2, self.y2))

    def __repr__(self):
        return f"Rectangle({self.x1}, {self.y1}, {self.x2}, {self.y2})"


def signed_area(ring: Sequence[Point]) -> Fraction:
    """Signed shoelace area: positive for CCW rings, negative for CW rings."""
    total = Fraction(0)
    n = len(ring)
    for k in range(n):
        x1, y1 = ring[k]
        x2, y2 = ring[(k + 1) % n]
        total += x1 * y2 - x2 * y1
    return total / 2


def _ring_length(ring: Sequence[Point]) -> Fraction:
    total = Fraction(0)
    n = len(ring)
    for k in range(n):
        x1, y1 = ring[k]
        x2, y2 = ring[(k + 1) % n]
        total += abs(x2 - x1) + abs(y2 - y1)
    return total


@dataclass(frozen=True)
class Polygon:
    """One connected component of the union: an outer ring plus its holes."""

    outer: Ring
    holes: Tuple[Ring, ...] = ()

    @property
    def area(self) -> Fraction:
        # Outer ring is CCW (positive), holes are CW (negative).
        return signed_area(self.outer) + sum(signed_area(h) for h in self.holes)

    @property
    def perimeter(self) -> Fraction:
        return _ring_length(self.outer) + sum(
            _ring_length(h) for h in self.holes
        )


@dataclass(frozen=True)
class UnionResult:
    area: Fraction
    perimeter: Fraction
    polygons: Tuple[Polygon, ...]


def _sharpest_left(edge, candidates):
    """Pick the outgoing edge making the sharpest left turn.

    At a vertex where filled cells touch only diagonally, two incoming and
    two outgoing boundary edges meet.  Always turning as far left as
    possible pairs the edges so the walk stays on one simple loop instead of
    crossing over into a self-touching figure-eight.
    """
    dx = _sign(edge[1][0] - edge[0][0])
    dy = _sign(edge[1][1] - edge[0][1])

    def turn_cross(c):
        cx = _sign(c[1][0] - c[0][0])
        cy = _sign(c[1][1] - c[0][1])
        return dx * cy - dy * cx  # +1 left, 0 straight, -1 right

    return max(candidates, key=turn_cross)


def _trace_rings(edges):
    """Stitch directed boundary edges (interior on the left) into loops."""
    outgoing = {}
    for e in edges:
        outgoing.setdefault(e[0], []).append(e)
    unused = set(edges)
    rings = []
    while unused:
        edge = min(unused)  # deterministic traversal order
        ring = [edge[0]]
        while True:
            unused.discard(edge)
            ring.append(edge[1])
            if edge[1] == ring[0]:
                break
            # In-degree equals out-degree at every vertex of a 2D boundary,
            # so an unused continuation always exists.
            candidates = [c for c in outgoing[edge[1]] if c in unused]
            edge = _sharpest_left(edge, candidates)
        ring.pop()  # drop the duplicated closing point
        rings.append(ring)
    return rings


def _split_loop(ring):
    """Split a closed walk at repeated vertices into simple loops.

    Corner contact and a hole pinching the outer boundary are locally
    identical (a diagonal checkerboard of filled cells), so no fixed local
    pairing rule can keep every traced walk simple.  Instead, any walk that
    revisits a vertex is decomposed here into simple closed sub-walks; edge
    directions are preserved, so each sub-loop keeps its interior-on-left
    orientation.
    """
    seen = {}
    for idx, vertex in enumerate(ring):
        if vertex in seen:
            start = seen[vertex]
            sub = ring[start:idx]  # closes back onto ``vertex``
            rest = ring[:start] + ring[idx:]
            return _split_loop(sub) + _split_loop(rest)
        seen[vertex] = idx
    return [ring]


def _drop_collinear(ring):
    """Remove vertices that lie on a straight run of boundary.

    Touch points shared with another ring are always genuine corners of
    their own ring (the sharpest-left rule turns there), so they survive.
    """
    pts = list(ring)
    while len(pts) > 2:
        kept = []
        n = len(pts)
        for i in range(n):
            ax, ay = pts[i - 1]
            bx, by = pts[i]
            cx, cy = pts[(i + 1) % n]
            cross = (bx - ax) * (cy - by) - (by - ay) * (cx - bx)
            if cross != 0:
                kept.append(pts[i])
        if len(kept) == len(pts):
            break
        pts = kept
    return pts


def _canonical(ring, want_ccw: bool) -> Ring:
    pts = list(ring)
    if (signed_area(pts) > 0) != want_ccw:
        pts.reverse()
    start = min(range(len(pts)), key=lambda i: pts[i])
    return tuple(pts[start:] + pts[:start])


def _point_in_ring(pt: Point, ring: Ring) -> bool:
    """Exact ray casting; ``pt`` must not lie on the ring itself."""
    x, y = pt
    inside = False
    n = len(ring)
    for k in range(n):
        (x1, y1), (x2, y2) = ring[k], ring[(k + 1) % n]
        if x1 != x2:
            continue  # only vertical edges cross a horizontal ray
        lo, hi = (y1, y2) if y1 < y2 else (y2, y1)
        if lo <= y < hi and x1 > x:
            inside = not inside
    return inside


def union(rectangles: Iterable) -> UnionResult:
    """Compute exact area, perimeter and outer contours of the union."""
    rects = [
        r if isinstance(r, Rectangle) else Rectangle(*r) for r in rectangles
    ]
    rects = [r for r in rects if not r.is_degenerate]
    if not rects:
        return UnionResult(Fraction(0), Fraction(0), ())

    xs = sorted({r.x1 for r in rects} | {r.x2 for r in rects})
    ys = sorted({r.y1 for r in rects} | {r.y2 for r in rects})
    nx, ny = len(xs) - 1, len(ys) - 1
    x_index = {x: i for i, x in enumerate(xs)}
    y_index = {y: j for j, y in enumerate(ys)}

    # Fill the elementary grid cells covered by at least one rectangle.
    filled = [[False] * ny for _ in range(nx)]
    for r in rects:
        for i in range(x_index[r.x1], x_index[r.x2]):
            column = filled[i]
            for j in range(y_index[r.y1], y_index[r.y2]):
                column[j] = True

    area = Fraction(0)
    edges = []
    for i in range(nx):
        xa, xb = xs[i], xs[i + 1]
        for j in range(ny):
            if not filled[i][j]:
                continue
            ya, yb = ys[j], ys[j + 1]
            area += (xb - xa) * (yb - ya)
            # Emit boundary edges directed so the filled interior is on the
            # left; this fixes ring orientation (outer CCW, holes CW).
            if j == 0 or not filled[i][j - 1]:
                edges.append(((xa, ya), (xb, ya)))  # bottom, eastward
            if j + 1 == ny or not filled[i][j + 1]:
                edges.append(((xb, yb), (xa, yb)))  # top, westward
            if i == 0 or not filled[i - 1][j]:
                edges.append(((xa, yb), (xa, ya)))  # left, southward
            if i + 1 == nx or not filled[i + 1][j]:
                edges.append(((xb, ya), (xb, yb)))  # right, northward

    perimeter = sum(
        abs(e[1][0] - e[0][0]) + abs(e[1][1] - e[0][1]) for e in edges
    )

    rings = [
        _drop_collinear(simple)
        for walked in _trace_rings(edges)
        for simple in _split_loop(walked)
    ]
    outers = [_canonical(r, want_ccw=True) for r in rings
              if signed_area(r) > 0]
    holes = [_canonical(r, want_ccw=False) for r in rings
             if signed_area(r) < 0]

    # Attach every hole to the smallest outer ring containing it.  The test
    # point is the midpoint of a hole edge: distinct rings can share vertices
    # but never edges, so this point is never on another ring.
    attached = {o: [] for o in outers}
    for h in holes:
        probe = ((h[0][0] + h[1][0]) / 2, (h[0][1] + h[1][1]) / 2)
        containers = [o for o in outers if _point_in_ring(probe, o)]
        owner = min(containers, key=lambda o: signed_area(o))
        attached[owner].append(h)

    polygons = tuple(
        Polygon(outer=o, holes=tuple(sorted(attached[o])))
        for o in sorted(outers)
    )
    return UnionResult(area=area, perimeter=perimeter, polygons=polygons)


def union_area(rectangles: Iterable) -> Fraction:
    return union(rectangles).area


def union_perimeter(rectangles: Iterable) -> Fraction:
    return union(rectangles).perimeter


def union_contours(rectangles: Iterable) -> Tuple[Polygon, ...]:
    return union(rectangles).polygons
