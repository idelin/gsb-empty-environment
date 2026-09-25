"""Exact triangulation of simple polygons with holes.

Pipeline: validate input -> bridge every hole into the outer ring ->
ear clipping on the resulting weakly simple ring.

Everything is computed with exact rational arithmetic; degenerate inputs
are defined precisely and rejected (or handled) without any epsilon:

* consecutive duplicate vertices are silently dropped (they carry no
  geometry);
* collinear vertices on a straight run are legal and are removed during
  ear clipping without emitting a triangle;
* a 180-degree spike (edge reversing onto its predecessor), a repeated
  vertex, a self-intersection, a zero-area ring, a hole touching/leaving
  the outer ring, or nested/touching holes are all rejected with
  ``PolygonError``.
"""
from dataclasses import dataclass
from fractions import Fraction

from geom import (INSIDE, OUTSIDE, as_point, orient, on_segment,
                  properly_intersect, collinearly_overlap, segments_intersect,
                  point_in_ring, signed_area2, point_strictly_in_triangle)


class PolygonError(ValueError):
    """The input polygon is malformed or degenerate."""


class TriangulationError(RuntimeError):
    """Triangulation failed despite validated input."""


@dataclass(frozen=True)
class Vertex:
    x: Fraction
    y: Fraction
    ring: int    # 0 = outer ring, k = k-th hole (1-based, input order)
    index: int   # index within the original input ring

    @property
    def point(self):
        return (self.x, self.y)


@dataclass(frozen=True)
class Triangle:
    """One output triangle, CCW, with provenance of each corner."""
    points: tuple  # ((x, y), (x, y), (x, y)) exact Fractions
    source: tuple  # ((ring, index), (ring, index), (ring, index))

    @property
    def area2(self):
        return orient(*self.points)

    @property
    def area(self):
        return self.area2 / 2


# ---------------------------------------------------------------- validation

def _normalize_ring(points, name):
    """Return [(orig_index, exact_point)] with duplicates removed."""
    cleaned = []
    for idx, p in enumerate(points):
        p = as_point(p)
        if cleaned and cleaned[-1][1] == p:
            continue
        cleaned.append((idx, p))
    if len(cleaned) > 1 and cleaned[0][1] == cleaned[-1][1]:
        cleaned.pop()
    if len(cleaned) < 3:
        raise PolygonError(f"{name}: need at least 3 distinct vertices")
    ring_pts = [p for _, p in cleaned]
    if len(set(ring_pts)) != len(ring_pts):
        raise PolygonError(f"{name}: repeated vertex")
    if signed_area2(ring_pts) == 0:
        raise PolygonError(f"{name}: zero-area ring")
    return cleaned


def _check_simple(ring_pts, name):
    """Reject self-intersections, touches and 180-degree spikes."""
    n = len(ring_pts)

    def edge(i):
        return ring_pts[i], ring_pts[(i + 1) % n]

    for i in range(n):
        a, b = edge(i)
        c = ring_pts[(i + 2) % n]
        if orient(a, b, c) == 0:
            dot = ((b[0] - a[0]) * (c[0] - b[0])
                   + (b[1] - a[1]) * (c[1] - b[1]))
            if dot < 0:
                raise PolygonError(f"{name}: 180-degree spike at vertex {b}")
    for i in range(n):
        for j in range(i + 1, n):
            if j == i + 1 or (i == 0 and j == n - 1):
                continue
            if segments_intersect(*edge(i), *edge(j)):
                raise PolygonError(f"{name}: edges {i} and {j} intersect")


def _check_holes(outer_pts, hole_pts_list):
    for k, hp in enumerate(hole_pts_list, 1):
        n, m = len(hp), len(outer_pts)
        for i in range(n):
            for j in range(m):
                if segments_intersect(hp[i], hp[(i + 1) % n],
                                      outer_pts[j], outer_pts[(j + 1) % m]):
                    raise PolygonError(f"hole {k} touches or crosses outer ring")
        if point_in_ring(hp[0], outer_pts) != INSIDE:
            raise PolygonError(f"hole {k} is not strictly inside outer ring")
    for a in range(len(hole_pts_list)):
        for b in range(a + 1, len(hole_pts_list)):
            ha, hb = hole_pts_list[a], hole_pts_list[b]
            for i in range(len(ha)):
                for j in range(len(hb)):
                    if segments_intersect(ha[i], ha[(i + 1) % len(ha)],
                                          hb[j], hb[(j + 1) % len(hb)]):
                        raise PolygonError(
                            f"holes {a + 1} and {b + 1} touch or cross")
            if point_in_ring(ha[0], hb) != OUTSIDE:
                raise PolygonError(f"hole {a + 1} nested inside hole {b + 1}")
            if point_in_ring(hb[0], ha) != OUTSIDE:
                raise PolygonError(f"hole {b + 1} nested inside hole {a + 1}")


def _prepare(outer, holes):
    rings = []
    o = _normalize_ring(outer, "outer")
    if signed_area2([p for _, p in o]) < 0:
        o.reverse()
    rings.append(o)
    for k, h in enumerate(holes, 1):
        hh = _normalize_ring(h, f"hole {k}")
        if signed_area2([p for _, p in hh]) > 0:
            hh.reverse()
        rings.append(hh)
    return rings


# ------------------------------------------------------------------- bridges

def _visible(h, v, obstacle_rings, outer_pts, all_hole_pts):
    """True iff the open segment h-v lies strictly inside the region."""
    p, q = h.point, v.point
    for ring in obstacle_rings:
        n = len(ring)
        for i in range(n):
            a = ring[i].point
            b = ring[(i + 1) % n].point
            if properly_intersect(p, q, a, b):
                return False
            if collinearly_overlap(p, q, a, b):
                return False
        for w in ring:
            if w is h or w is v:
                continue
            if orient(p, q, w.point) == 0 and on_segment(p, q, w.point):
                return False
    mid = ((p[0] + q[0]) / 2, (p[1] + q[1]) / 2)
    if point_in_ring(mid, outer_pts) != INSIDE:
        return False
    for hp in all_hole_pts:
        if point_in_ring(mid, hp) != OUTSIDE:
            return False
    return True


def _bridge(merged, hole, other_holes, outer_pts, all_hole_pts):
    """Splice ``hole`` into the ``merged`` ring via a legal bridge.

    The bridge endpoint is the hole vertex with maximal (x, y); the target
    is the nearest strictly visible vertex of the merged ring.  Both
    endpoints are duplicated in the resulting ring (weakly simple), which
    is exactly what the ear clipper expects.
    """
    hi = max(range(len(hole)), key=lambda i: (hole[i].x, hole[i].y))
    h = hole[hi]
    obstacle_rings = [merged, hole] + list(other_holes)
    best = best_key = None
    for v in merged:
        if not _visible(h, v, obstacle_rings, outer_pts, all_hole_pts):
            continue
        d2 = (h.x - v.x) ** 2 + (h.y - v.y) ** 2
        key = (d2, v.ring, v.index)
        if best_key is None or key < best_key:
            best_key, best = key, v
    if best is None:
        raise TriangulationError("no visible vertex found for hole bridge")
    vi = merged.index(best)
    rot = hole[hi:] + hole[:hi]
    return merged[:vi + 1] + rot + [rot[0], best] + merged[vi + 1:]


# --------------------------------------------------------------- ear clipping

def _is_ear(ring, i):
    n = len(ring)
    a = ring[(i - 1) % n].point
    b = ring[i].point
    c = ring[(i + 1) % n].point
    for j in range(n):
        if j in ((i - 1) % n, i, (i + 1) % n):
            continue
        w = ring[j].point
        if w == a or w == b or w == c:
            continue  # coincident duplicate from a bridge; cannot be inside
        if point_strictly_in_triangle(w, a, b, c):
            return False
    return True


def _make_triangle(a, b, c):
    return Triangle(
        points=(a.point, b.point, c.point),
        source=((a.ring, a.index), (b.ring, b.index), (c.ring, c.index)),
    )


def _ear_clip(verts):
    ring = list(verts)
    triangles = []
    while len(ring) > 3:
        n = len(ring)
        clipped = False
        for i in range(n):
            prev = ring[(i - 1) % n]
            cur = ring[i]
            nxt = ring[(i + 1) % n]
            o = orient(prev.point, cur.point, nxt.point)
            if o < 0:
                continue  # reflex vertex
            if o == 0:
                # Collinear vertex on a straight run: drop it, emit nothing.
                if on_segment(prev.point, nxt.point, cur.point):
                    del ring[i]
                    clipped = True
                    break
                continue  # spike: cannot happen for validated input
            if _is_ear(ring, i):
                triangles.append(_make_triangle(prev, cur, nxt))
                del ring[i]
                clipped = True
                break
        if not clipped:
            raise TriangulationError("ear clipping stuck on validated input")
    if orient(ring[0].point, ring[1].point, ring[2].point) > 0:
        triangles.append(_make_triangle(ring[0], ring[1], ring[2]))
    return triangles


# ------------------------------------------------------------------ interface

def triangulate(outer, holes=None):
    """Triangulate a simple polygon with holes.

    ``outer`` and each entry of ``holes`` are sequences of (x, y) pairs of
    ints or ``Fraction``s.  Returns a list of ``Triangle`` whose exact areas
    sum to area(outer) - sum(area(hole)).  Raises ``PolygonError`` on
    degenerate input (see module docstring for the exact definitions).
    """
    holes = list(holes or [])
    rings = _prepare(outer, holes)
    outer_pts = [p for _, p in rings[0]]
    _check_simple(outer_pts, "outer")
    hole_pts_list = []
    for k, hk in enumerate(rings[1:], 1):
        pts = [p for _, p in hk]
        _check_simple(pts, f"hole {k}")
        hole_pts_list.append(pts)
    _check_holes(outer_pts, hole_pts_list)

    vrings = []
    for k, ring in enumerate(rings):
        vrings.append([Vertex(p[0], p[1], k, idx) for idx, p in ring])

    merged = list(vrings[0])
    order = sorted(range(len(holes)),
                   key=lambda k: max((v.x, v.y) for v in vrings[k + 1]),
                   reverse=True)
    remaining = set(order)
    for k in order:
        others = [vrings[j + 1] for j in sorted(remaining) if j != k]
        merged = _bridge(merged, vrings[k + 1], others,
                         outer_pts, hole_pts_list)
        remaining.discard(k)
    return _ear_clip(merged)
