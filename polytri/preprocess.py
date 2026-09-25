"""Input normalisation and exact validity checks for polygons with holes.

Degenerate inputs are *defined*, not patched over with epsilons:

* consecutive duplicate vertices and collinear vertices that lie on the
  segment between their neighbours are removed exactly (this never changes
  the described region);
* anything else degenerate (self-intersection, self-touch, spikes, zero-area
  rings, holes touching the outer ring or each other, nested holes) raises
  ``PolygonError``.
"""

from .exact import (
    INSIDE,
    OUTSIDE,
    area2,
    classify_point,
    orient,
    segments_intersect,
    to_point,
)


class PolygonError(ValueError):
    """Raised when an input polygon is degenerate or self-intersecting."""


def ring_edges(ring):
    """Closed edges (p_i, p_{i+1}) of a ring."""
    n = len(ring)
    return [(ring[i], ring[(i + 1) % n]) for i in range(n)]


def _between(a, b, c):
    """True iff b lies on the closed segment a-c (a, b, c known collinear)."""
    return (b[0] - a[0]) * (c[0] - b[0]) + (b[1] - a[1]) * (c[1] - b[1]) >= 0


def clean_ring(ring):
    """Return an exact copy of ``ring`` without redundant vertices.

    Removes consecutive duplicates and collinear vertices that lie on the
    segment between their neighbours.  Both operations are exact and leave
    the described region unchanged.
    """
    pts = [to_point(p) for p in ring]
    out = []
    for p in pts:
        if not out or p != out[-1]:
            out.append(p)
    while len(out) > 1 and out[0] == out[-1]:
        out.pop()
    changed = True
    while changed and len(out) > 2:
        changed = False
        n = len(out)
        for i in range(n):
            a, b, c = out[(i - 1) % n], out[i], out[(i + 1) % n]
            if orient(a, b, c) == 0 and _between(a, b, c):
                del out[i]
                changed = True
                break
    return out


def _check_simple(ring):
    """Raise PolygonError unless ``ring`` is a strictly simple polygon."""
    n = len(ring)
    seen = set()
    for p in ring:
        if p in seen:
            raise PolygonError(f"duplicate vertex {_fmt(p)}")
        seen.add(p)
    for i in range(n):
        if orient(ring[i], ring[(i + 1) % n], ring[(i + 2) % n]) == 0:
            raise PolygonError(
                f"spike or 180-degree turn at vertex {_fmt(ring[(i + 1) % n])}"
            )
    edges = ring_edges(ring)
    for i in range(n):
        for j in range(i + 1, n):
            if j == i + 1 or (i == 0 and j == n - 1):
                continue
            if segments_intersect(*edges[i], *edges[j]):
                raise PolygonError(
                    "ring self-intersects or touches itself between "
                    f"{_fmt(edges[i][0])}->{_fmt(edges[i][1])} and "
                    f"{_fmt(edges[j][0])}->{_fmt(edges[j][1])}"
                )


def _fmt(p):
    return f"({p[0]}, {p[1]})"


def normalize_polygon(outer, holes=()):
    """Validate and normalise a polygon with holes.

    Returns ``(outer_ring, hole_rings)`` where the outer ring is CCW and
    every hole is CW, all cleaned of redundant vertices.  Raises
    ``PolygonError`` for any degenerate or invalid configuration.
    """
    outer_ring = clean_ring(outer)
    if len(outer_ring) < 3:
        raise PolygonError("outer ring degenerates to fewer than 3 vertices")
    if area2(outer_ring) == 0:
        raise PolygonError("outer ring has zero area")
    _check_simple(outer_ring)
    if area2(outer_ring) < 0:
        outer_ring = list(reversed(outer_ring))

    hole_rings = []
    for k, hole in enumerate(holes):
        h = clean_ring(hole)
        if len(h) < 3:
            raise PolygonError(f"hole {k} degenerates to fewer than 3 vertices")
        if area2(h) == 0:
            raise PolygonError(f"hole {k} has zero area")
        _check_simple(h)
        if area2(h) > 0:
            h = list(reversed(h))
        hole_rings.append(h)

    outer_edges = ring_edges(outer_ring)
    for k, h in enumerate(hole_rings):
        for p in h:
            if classify_point(outer_ring, p) != INSIDE:
                raise PolygonError(
                    f"hole {k} is not strictly inside the outer ring"
                )
        for e in ring_edges(h):
            for oe in outer_edges:
                if segments_intersect(*e, *oe):
                    raise PolygonError(f"hole {k} intersects the outer ring")

    for k1 in range(len(hole_rings)):
        for k2 in range(k1 + 1, len(hole_rings)):
            h1, h2 = hole_rings[k1], hole_rings[k2]
            for e1 in ring_edges(h1):
                for e2 in ring_edges(h2):
                    if segments_intersect(*e1, *e2):
                        raise PolygonError(
                            f"holes {k1} and {k2} intersect or touch"
                        )
            if (
                classify_point(h2, h1[0]) != OUTSIDE
                or classify_point(h1, h2[0]) != OUTSIDE
            ):
                raise PolygonError(f"holes {k1} and {k2} overlap or nest")

    return outer_ring, hole_rings
