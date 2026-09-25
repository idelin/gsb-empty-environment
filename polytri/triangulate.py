"""Triangulation of simple polygons with holes.

Strategy
--------
1. The input is normalised and validated (see :mod:`polytri.preprocess`).
2. Every hole is merged into the outer ring by a *bridge*: a segment from a
   hole vertex to a vertex of the current outer ring whose open segment
   touches no boundary edge at all (checked exactly).  The hole is spliced
   in with both bridge endpoints duplicated, producing one weakly simple
   ring with a zero-width slit.
3. The resulting ring is triangulated by ear clipping.  An ear must be
   strictly convex, contain no other vertex (inclusive test, with the
   bridge's duplicate coordinates of its own corners exempted), have a
   diagonal that properly crosses no edge, and have its diagonal midpoint
   strictly inside the ring.  Only triangles with strictly positive area
   are emitted, so duplicated bridge vertices can never yield zero-area
   output.

Every test uses exact rational arithmetic; no epsilon appears anywhere.
"""

from dataclasses import dataclass
from fractions import Fraction

from .exact import (
    INSIDE,
    classify_point,
    collinear_relation,
    in_triangle_inclusive,
    on_segment,
    orient,
    proper_cross,
)
from .preprocess import normalize_polygon, ring_edges


class TriangulationError(RuntimeError):
    """Raised when no legal bridge or ear can be found."""


@dataclass(frozen=True)
class Triangle:
    """One output triangle.

    ``vertices`` are exact points in CCW order; ``sources`` is the sorted
    tuple of origin labels of its corners (``"outer"`` / ``"hole[k]"``);
    ``area`` is the exact positive area.
    """

    vertices: tuple
    sources: tuple
    area: Fraction

    @property
    def source_label(self):
        """Human-readable provenance, e.g. ``outer`` or ``hole[0]+outer``."""
        return "+".join(self.sources)


class _Node:
    __slots__ = ("co", "origin")

    def __init__(self, co, origin):
        self.co = co
        self.origin = origin


def _dist2(p, q):
    return (p[0] - q[0]) ** 2 + (p[1] - q[1]) ** 2


def _bridge_clear(p, q, edges):
    """True iff the open segment p-q touches no edge, except at shared endpoints.

    Any contact other than a shared segment endpoint is fatal: proper
    crossings, collinear overlaps, passing through a vertex, or an endpoint
    landing in the interior of an edge.
    """
    for a, b in edges:
        o1 = orient(a, b, p)
        o2 = orient(a, b, q)
        if o1 == 0 and o2 == 0:
            kind, r = collinear_relation(p, q, a, b)
            if kind == "segment":
                return False
            if kind == "point" and not (r in (p, q) and r in (a, b)):
                return False
            continue
        if proper_cross(p, q, a, b):
            return False
        if o1 == 0 and on_segment(a, b, p) and p != a and p != b:
            return False
        if o2 == 0 and on_segment(a, b, q) and q != a and q != b:
            return False
        o3 = orient(p, q, a)
        if o3 == 0 and on_segment(p, q, a) and a != p and a != q:
            return False
        o4 = orient(p, q, b)
        if o4 == 0 and on_segment(p, q, b) and b != p and b != q:
            return False
    return True


def _find_bridge(ring, hole, edges):
    """Find (hole_index, ring_index) of a legal bridge, deterministically.

    Hole vertices are tried rightmost-first (then topmost); ring vertices
    are tried by increasing squared distance from the hole vertex.
    """
    h_order = sorted(
        range(len(hole)), key=lambda i: (-hole[i].co[0], -hole[i].co[1], i)
    )
    for hi in h_order:
        h = hole[hi].co
        w_order = sorted(range(len(ring)), key=lambda i: (_dist2(h, ring[i].co), i))
        for wi in w_order:
            if _bridge_clear(h, ring[wi].co, edges):
                return hi, wi
    raise TriangulationError("no legal hole bridge found")


def _merge(ring, wi, hole, hi):
    """Splice ``hole`` into ``ring`` across the bridge ring[wi] <-> hole[hi].

    Both bridge endpoints appear twice in the merged ring (the slit); the
    hole is traversed in its own (clockwise) orientation so the region
    stays on the left side of the walk.
    """
    w = ring[wi]
    h = hole[hi]
    h_dup = _Node(h.co, h.origin)
    w_dup = _Node(w.co, w.origin)
    rotated = hole[hi + 1 :] + hole[:hi]
    return ring[: wi + 1] + [h] + rotated + [h_dup, w_dup] + ring[wi + 1 :]


def _is_ear(a, b, c, remaining, coords, edges):
    """Strict ear test for the triangle (a, b, c) in the current ring."""
    if orient(a.co, b.co, c.co) <= 0:
        return False
    corners = (a.co, b.co, c.co)
    for u in remaining:
        if u is a or u is b or u is c or u.co in corners:
            # Bridge duplicates share coordinates with a corner; they sit
            # exactly on that corner, never strictly inside the triangle.
            continue
        if in_triangle_inclusive(u.co, a.co, b.co, c.co):
            return False
    for e0, e1 in edges:
        if proper_cross(a.co, c.co, e0, e1):
            return False
    mid = ((a.co[0] + c.co[0]) / 2, (a.co[1] + c.co[1]) / 2)
    if classify_point(coords, mid) != INSIDE:
        return False
    return True


def _ear_clip(nodes):
    """Ear-clipping triangulation of a (weakly) simple ring of nodes."""
    remaining = list(nodes)
    triangles = []
    while len(remaining) > 3:
        m = len(remaining)
        coords = [v.co for v in remaining]
        edges = [(coords[i], coords[(i + 1) % m]) for i in range(m)]
        clipped = False
        for i in range(m):
            a = remaining[(i - 1) % m]
            b = remaining[i]
            c = remaining[(i + 1) % m]
            if _is_ear(a, b, c, remaining, coords, edges):
                triangles.append((a, b, c))
                del remaining[i]
                clipped = True
                break
        if not clipped:
            raise TriangulationError("no legal ear found (degenerate input?)")
    if len(remaining) == 3:
        a, b, c = remaining
        # The final triple may collapse onto duplicated bridge vertices;
        # only a positively oriented triple is a real triangle.
        if orient(a.co, b.co, c.co) > 0:
            triangles.append((a, b, c))
    return triangles


def triangulate(outer, holes=()):
    """Triangulate a simple polygon with holes.

    ``outer`` and each entry of ``holes`` are sequences of 2D points with
    int / Fraction coordinates.  Returns a list of :class:`Triangle`.
    Raises ``PolygonError`` for invalid input and ``TriangulationError``
    if no legal bridge or ear exists.
    """
    outer_ring, hole_rings = normalize_polygon(outer, holes)
    ring = [_Node(p, "outer") for p in outer_ring]
    hole_nodes = [
        [_Node(p, f"hole[{k}]") for p in hring]
        for k, hring in enumerate(hole_rings)
    ]

    def rightmost_key(nodes):
        return max((n.co[0], n.co[1]) for n in nodes)

    order = sorted(range(len(hole_nodes)), key=lambda k: rightmost_key(hole_nodes[k]), reverse=True)
    pending = set(order)
    for k in order:
        hole = hole_nodes[k]
        edges = ring_edges([n.co for n in ring])
        for j in sorted(pending):
            edges += ring_edges([n.co for n in hole_nodes[j]])
        hi, wi = _find_bridge(ring, hole, edges)
        ring = _merge(ring, wi, hole, hi)
        pending.discard(k)

    triangles = []
    for a, b, c in _ear_clip(ring):
        triangles.append(
            Triangle(
                vertices=(a.co, b.co, c.co),
                sources=tuple(sorted({a.origin, b.origin, c.origin})),
                area=orient(a.co, b.co, c.co) / 2,
            )
        )
    return triangles
