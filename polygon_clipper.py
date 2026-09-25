"""Exact clipping of a simple polygon by a convex clipping window.

All computations use exact rational arithmetic (``fractions.Fraction``), so
every topological decision (orientation, on-segment, intersection tests) is
an exact predicate.  No epsilon tolerances are used anywhere.

Semantics and degenerate-case definitions
-----------------------------------------
* Subject and window are treated as *closed* sets: a point exactly on an
  edge belongs to the region.  The window is the intersection of the closed
  half-planes of its edges, so subject boundary that coincides with a window
  edge is kept, never dropped.
* The subject must be a simple polygon: at least 3 distinct vertices, no
  repeated vertices, non-adjacent edges may not intersect or touch, and
  consecutive edges may not fold back onto each other.  Violations raise
  :class:`SelfIntersectionError`.  Zero-area input raises
  :class:`DegenerateInputError`.
* The window must be a simple, convex polygon with non-zero area
  (:class:`NonConvexWindowError` otherwise).  Either vertex winding is
  accepted for both operands.
* The result is a tuple of regions.  A region has kind ``"polygon"``
  (positive area), or the degenerate kinds ``"line"`` / ``"point"`` which
  are produced when (part of) the intersection collapses to a segment or a
  single point.  Zero-width "bridge" segments that merely connect two
  positive-area components of the result are absorbed and not reported as
  separate regions.
* Every output edge records its provenance: the indices of the subject
  edges and of the window edges it lies on.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

Point = tuple[Fraction, Fraction]

__all__ = [
    "Point",
    "EdgeSource",
    "Region",
    "ClipResult",
    "clip_polygon",
    "signed_area",
    "polygon_area",
    "PolygonClipperError",
    "SelfIntersectionError",
    "DegenerateInputError",
    "NonConvexWindowError",
]


# ---------------------------------------------------------------------------
# errors
# ---------------------------------------------------------------------------

class PolygonClipperError(ValueError):
    """Base class for polygon clipper input errors."""


class SelfIntersectionError(PolygonClipperError):
    """The polygon is not simple (it crosses or touches itself)."""


class DegenerateInputError(PolygonClipperError):
    """The polygon is degenerate (too few vertices or zero area)."""


class NonConvexWindowError(PolygonClipperError):
    """The clipping window is not a convex polygon."""


# ---------------------------------------------------------------------------
# coordinate handling
# ---------------------------------------------------------------------------

def _to_coord(value, where):
    if isinstance(value, bool):
        raise TypeError(f"{where}: boolean is not a valid coordinate")
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        try:
            return Fraction(value)
        except (ValueError, ZeroDivisionError) as exc:
            raise TypeError(
                f"{where}: cannot parse {value!r} as a rational") from exc
    raise TypeError(
        f"{where}: coordinates must be int, Fraction or rational strings, "
        f"got {type(value).__name__}")


def _to_point(raw, where):
    if isinstance(raw, (str, bytes)):
        raise TypeError(f"{where}: expected an (x, y) pair, got {raw!r}")
    try:
        x, y = raw
    except (TypeError, ValueError) as exc:
        raise TypeError(f"{where}: expected an (x, y) pair, got {raw!r}") from exc
    return (_to_coord(x, where), _to_coord(y, where))


# ---------------------------------------------------------------------------
# exact geometric predicates
# ---------------------------------------------------------------------------

def _orient(a, b, c):
    """Exact orientation: >0 if c is left of a->b, <0 if right, 0 if collinear."""
    return (b[0] - a[0]) * (c[1] - a[1]) - (b[1] - a[1]) * (c[0] - a[0])


def _strictly_between(a, b, p):
    """True if p lies strictly between a and b (collinearity assumed)."""
    return ((a[0] - p[0]) * (b[0] - p[0])
            + (a[1] - p[1]) * (b[1] - p[1])) < 0


def _on_segment(a, b, p):
    """True if p lies on the closed segment ab (collinearity assumed)."""
    return (min(a[0], b[0]) <= p[0] <= max(a[0], b[0])
            and min(a[1], b[1]) <= p[1] <= max(a[1], b[1]))


def _segments_intersect(p1, p2, q1, q2):
    """Exact segment intersection test, including endpoint touches/overlaps."""
    d1 = _orient(q1, q2, p1)
    d2 = _orient(q1, q2, p2)
    d3 = _orient(p1, p2, q1)
    d4 = _orient(p1, p2, q2)
    if d1 == 0 and _on_segment(q1, q2, p1):
        return True
    if d2 == 0 and _on_segment(q1, q2, p2):
        return True
    if d3 == 0 and _on_segment(p1, p2, q1):
        return True
    if d4 == 0 and _on_segment(p1, p2, q2):
        return True
    return (((d1 > 0) != (d2 > 0)) and d1 != 0 and d2 != 0
            and ((d3 > 0) != (d4 > 0)) and d3 != 0 and d4 != 0)


def signed_area(vertices):
    """Exact signed area (positive for counter-clockwise vertex order)."""
    total = Fraction(0)
    n = len(vertices)
    for i in range(n):
        x1, y1 = vertices[i]
        x2, y2 = vertices[(i + 1) % n]
        total += x1 * y2 - x2 * y1
    return total / 2


def polygon_area(vertices):
    """Exact (unsigned) area of a polygon given by its vertices."""
    return abs(signed_area(vertices))


# ---------------------------------------------------------------------------
# input validation
# ---------------------------------------------------------------------------

def _check_simple(pts, name):
    n = len(pts)
    if n < 3:
        raise DegenerateInputError(
            f"{name}: need at least 3 vertices, got {n}")
    if len(set(pts)) != n:
        raise SelfIntersectionError(f"{name}: repeated vertices")
    edges = [(pts[i], pts[(i + 1) % n]) for i in range(n)]
    for i in range(n):
        a, b = edges[i]
        for j in range(i + 1, n):
            c, d = edges[j]
            if j == i + 1 or (i == 0 and j == n - 1):
                # Adjacent edges share exactly one endpoint (vertices are
                # distinct).  They are invalid only if they are collinear and
                # overlap, i.e. the shared endpoint is not strictly between
                # the two free endpoints.
                if j == i + 1:
                    shared, free1, free2 = b, a, d
                else:
                    shared, free1, free2 = a, b, c
                if (_orient(shared, free1, free2) == 0
                        and not _strictly_between(free1, free2, shared)):
                    raise SelfIntersectionError(
                        f"{name}: consecutive edges {i} and {j} overlap")
            elif _segments_intersect(a, b, c, d):
                raise SelfIntersectionError(
                    f"{name}: edges {i} and {j} intersect")
    if signed_area(pts) == 0:
        raise DegenerateInputError(f"{name}: polygon has zero area")


def _check_convex(pts):
    """Validate convexity; return the winding sign (+1 ccw / -1 cw)."""
    n = len(pts)
    sign = 0
    for i in range(n):
        turn = _orient(pts[i], pts[(i + 1) % n], pts[(i + 2) % n])
        if turn == 0:
            continue
        cur = 1 if turn > 0 else -1
        if sign == 0:
            sign = cur
        elif cur != sign:
            raise NonConvexWindowError("window: polygon is not convex")
    return sign  # never 0: the window has non-zero area


# ---------------------------------------------------------------------------
# Sutherland-Hodgman clipping against one closed half-plane
# ---------------------------------------------------------------------------

def _intersection_point(s, e, a, b):
    """Exact intersection of segment s->e with the line through a and b."""
    os_ = _orient(a, b, s)
    oe = _orient(a, b, e)
    t = os_ / (os_ - oe)
    return (s[0] + t * (e[0] - s[0]), s[1] + t * (e[1] - s[1]))


def _clip_halfplane(poly, a, b, wsign):
    """Keep the closed half-plane left of a->b (right of it if wsign < 0)."""
    def inside(p):
        return _orient(a, b, p) * wsign >= 0

    out = []
    n = len(poly)
    for i in range(n):
        s = poly[i - 1]
        e = poly[i]
        s_in = inside(s)
        e_in = inside(e)
        if e_in:
            if not s_in:
                out.append(_intersection_point(s, e, a, b))
            out.append(e)
        elif s_in:
            out.append(_intersection_point(s, e, a, b))
    return out


# ---------------------------------------------------------------------------
# turning the weakly-simple clip result into simple loops
# ---------------------------------------------------------------------------

def _dedupe_cyclic(loop):
    out = []
    for p in loop:
        if not out or out[-1] != p:
            out.append(p)
    while len(out) > 1 and out[0] == out[-1]:
        out.pop()
    return out


def _split_at_interior_vertices(loop):
    """Split edges at every loop vertex lying in their interior (exactly)."""
    loop = list(loop)
    again = True
    while again:
        again = False
        n = len(loop)
        for i in range(n):
            a = loop[i]
            b = loop[(i + 1) % n]
            if a == b:
                continue
            for p in loop:
                if (p != a and p != b and _orient(a, b, p) == 0
                        and _strictly_between(a, b, p)):
                    loop.insert(i + 1, p)
                    again = True
                    break
            if again:
                break
    return loop


def _split_bridges(loop):
    """Split a weakly-simple loop at zero-width bridges into simple loops.

    A bridge is a segment traversed twice in opposite directions; removing
    such a pair of edges splits the loop into two independent loops.
    """
    stack = [loop]
    out = []
    while stack:
        cur = stack.pop()
        n = len(cur)
        if n <= 2:
            out.append(cur)
            continue
        seen = {}
        split = None
        for i in range(n):
            a = cur[i]
            b = cur[(i + 1) % n]
            j = seen.get((b, a))
            if j is not None:
                split = (j, i)
                break
            seen[(a, b)] = i
        if split is None:
            out.append(cur)
        else:
            j, i = split
            for part in (cur[j + 1:i + 1], cur[i + 1:] + cur[:j + 1]):
                part = _dedupe_cyclic(part)
                if part:
                    stack.append(part)
    return out


# ---------------------------------------------------------------------------
# output regions and provenance
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class EdgeSource:
    """One output edge and the input edges it originates from."""
    a: Point
    b: Point
    subject_edges: tuple  # indices of subject edges containing a->b
    window_edges: tuple   # indices of window edges containing a->b


@dataclass(frozen=True)
class Region:
    """One connected component of the clipped result.

    ``kind`` is ``"polygon"`` (positive area), ``"line"`` or ``"point"``
    (degenerate, zero area).  ``vertices`` holds the polygon loop, the two
    line endpoints, or the single point.  ``edges`` carries per-edge
    provenance for polygon/line regions; for point regions
    ``on_subject_edges``/``on_window_edges`` list the input edges the point
    lies on.
    """
    kind: str
    vertices: tuple
    area: Fraction
    edges: tuple
    on_subject_edges: tuple = ()
    on_window_edges: tuple = ()

    @property
    def degenerate(self):
        return self.kind != "polygon"


@dataclass(frozen=True)
class ClipResult:
    regions: tuple

    @property
    def polygons(self):
        return tuple(r for r in self.regions if r.kind == "polygon")

    @property
    def degenerate(self):
        return tuple(r for r in self.regions if r.kind != "polygon")

    @property
    def total_area(self):
        return sum((r.area for r in self.regions), Fraction(0))


def _edges_of(pts):
    return [(pts[i], pts[(i + 1) % len(pts)]) for i in range(len(pts))]


def _carrying_edges(a, b, poly):
    """Indices of edges of poly that contain the whole segment a->b."""
    idx = []
    for k, (p, q) in enumerate(_edges_of(poly)):
        if (_orient(p, q, a) == 0 and _orient(p, q, b) == 0
                and _on_segment(p, q, a) and _on_segment(p, q, b)):
            idx.append(k)
    return idx


def _containing_edges(p, poly):
    """Indices of edges of poly that contain the point p."""
    idx = []
    for k, (a, b) in enumerate(_edges_of(poly)):
        if _orient(a, b, p) == 0 and _on_segment(a, b, p):
            idx.append(k)
    return idx


def _extremes(pts):
    """The two points of pts with maximal mutual distance."""
    best = (pts[0], pts[0])
    best_d = Fraction(-1)
    for i in range(len(pts)):
        for j in range(i + 1, len(pts)):
            d = ((pts[i][0] - pts[j][0]) ** 2
                 + (pts[i][1] - pts[j][1]) ** 2)
            if d > best_d:
                best_d = d
                best = (pts[i], pts[j])
    return best


def _make_region(loop, subject, window):
    pts = _dedupe_cyclic(loop)
    if len(set(pts)) == 1:
        p = pts[0]
        return Region("point", (p,), Fraction(0), (),
                      tuple(_containing_edges(p, subject)),
                      tuple(_containing_edges(p, window)))
    if len(pts) == 2 or signed_area(pts) == 0:
        a, b = _extremes(pts)
        source = EdgeSource(a, b,
                            tuple(_carrying_edges(a, b, subject)),
                            tuple(_carrying_edges(a, b, window)))
        return Region("line", (a, b), Fraction(0), (source,))
    edges = []
    n = len(pts)
    for i in range(n):
        a, b = pts[i], pts[(i + 1) % n]
        edges.append(EdgeSource(a, b,
                                tuple(_carrying_edges(a, b, subject)),
                                tuple(_carrying_edges(a, b, window))))
    return Region("polygon", tuple(pts), abs(signed_area(pts)), tuple(edges))


# ---------------------------------------------------------------------------
# public entry point
# ---------------------------------------------------------------------------

def clip_polygon(subject, window):
    """Clip the simple polygon ``subject`` by the convex ``window``.

    Both operands are sequences of (x, y) pairs with int / Fraction /
    rational-string coordinates.  Returns a :class:`ClipResult`.
    """
    subj = tuple(_to_point(p, f"subject[{k}]") for k, p in enumerate(subject))
    win = tuple(_to_point(p, f"window[{k}]") for k, p in enumerate(window))
    _check_simple(subj, "subject")
    _check_simple(win, "window")
    wsign = _check_convex(win)

    poly = list(subj)
    n = len(win)
    for k in range(n):
        poly = _clip_halfplane(poly, win[k], win[(k + 1) % n], wsign)
        if not poly:
            break
    poly = _dedupe_cyclic(poly)
    if len(poly) >= 3:
        poly = _split_at_interior_vertices(poly)
    loops = _split_bridges(poly) if poly else []
    regions = tuple(_make_region(loop, subj, win) for loop in loops if loop)
    return ClipResult(regions)
