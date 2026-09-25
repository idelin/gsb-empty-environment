"""Topology-preserving simplification of polylines with shared boundaries.

Guarantees (all decided with exact rational arithmetic, no epsilon):

* Every removed point has error <= threshold at removal time, measured as the
  exact distance to the shortcut segment that replaces it.
* A removal that changes the geometry at all is only applied when the swept
  triangle contains no vertex of the whole arrangement and the shortcut
  segment touches/crosses no retained segment.  Hence no new intersections,
  no new touches, no sealed corridors, and no flipped rings are introduced.
* Removals that leave the geometry point-set unchanged (strictly redundant
  points: duplicates and collinear points between their neighbours) are
  always allowed; at threshold 0 exactly those points are removed.
* Chains shared by several polylines (common seams) are simplified exactly
  once, so all regions keep byte-identical shared boundaries.
* Closed rings keep at least 3 vertices and never change the sign of their
  signed area.

The module also reports, for every removed point, the exact squared distance
to the *final* simplified chain (a certified a-posteriori error bound).
"""

from fractions import Fraction
from typing import NamedTuple

from exactgeom import (
    dist2,
    on_segment,
    orient,
    point_in_closed_triangle,
    point_segment_dist2,
    segments_intersect,
    sign,
)

Point = tuple  # (Fraction, Fraction)


class RemovedPoint(NamedTuple):
    point: Point
    chain_id: int
    dist2_to_result: Fraction  # exact squared distance to the final chain


class SimplifyResult(NamedTuple):
    polylines: list          # simplified polylines (rings have no repeated end)
    closed: list             # closed flag per polyline
    removed: list            # list of RemovedPoint, in removal order
    max_error2: Fraction     # certified bound: max squared dist to final chain
    threshold2: Fraction     # squared threshold actually used


def _pt(p):
    return (Fraction(p[0]), Fraction(p[1]))


class _Chain:
    """A maximal run of vertices between junctions (or a whole ring)."""

    __slots__ = ("points", "closed", "cid")

    def __init__(self, cid, points, closed):
        self.cid = cid
        self.points = points
        self.closed = closed


def _signed_area2(pts):
    s = Fraction(0)
    n = len(pts)
    for i in range(n):
        x1, y1 = pts[i]
        x2, y2 = pts[(i + 1) % n]
        s += x1 * y2 - x2 * y1
    return s


def _chain_segments(ch):
    pts = ch.points
    n = len(pts)
    m = n if ch.closed else n - 1
    out = []
    for i in range(m):
        a, b = pts[i], pts[(i + 1) % n]
        if a != b:
            out.append((a, b))
    return out


def _build_chains(polylines, closed):
    """Split polylines into chains at junction vertices.

    A vertex is a junction iff its degree in the union graph (number of
    distinct adjacent vertices, ignoring zero-length edges) differs from 2,
    or it is an endpoint of an open polyline.  Chains with identical vertex
    sequences (up to reversal) are unified into a single shared chain.
    """
    neighbours = {}
    for pts, cl in zip(polylines, closed):
        n = len(pts)
        rng = range(n) if cl else range(n - 1)
        for i in rng:
            a, b = pts[i], pts[(i + 1) % n]
            if a == b:
                continue
            neighbours.setdefault(a, set()).add(b)
            neighbours.setdefault(b, set()).add(a)

    def is_junction(v, is_endpoint):
        return is_endpoint or len(neighbours.get(v, ())) != 2

    chains = []
    registry = {}

    def register(seq):
        key = tuple(seq)
        if key in registry:
            return registry[key], False
        rkey = key[::-1]
        if rkey in registry:
            return registry[rkey], True
        ch = _Chain(len(chains), list(seq), False)
        chains.append(ch)
        registry[key] = ch
        return ch, False

    refs_per_poly = []
    for pts, cl in zip(polylines, closed):
        n = len(pts)
        refs = []
        if n == 0:
            refs_per_poly.append(refs)
            continue
        if cl:
            jidx = [i for i in range(n) if is_junction(pts[i], False)]
            if not jidx:
                ch = _Chain(len(chains), list(pts), True)
                chains.append(ch)
                refs.append((ch, False))
            else:
                j0 = jidx[0]
                rot = pts[j0:] + pts[:j0]
                pos = [(j - j0) % n for j in jidx]
                m = len(pos)
                for k in range(m):
                    a = pos[k]
                    b = pos[(k + 1) % m]
                    if b <= a:
                        b += n
                    seq = [rot[t % n] for t in range(a, b + 1)]
                    refs.append(register(seq))
        else:
            jidx = [0] + [i for i in range(1, n - 1)
                          if is_junction(pts[i], False)] + [n - 1]
            for k in range(len(jidx) - 1):
                refs.append(register(pts[jidx[k]:jidx[k + 1] + 1]))
        refs_per_poly.append(refs)
    return chains, refs_per_poly


def _removal_ok(ch, i, chains):
    """Decide whether removing vertex i of chain ch preserves topology."""
    pts = ch.points
    n = len(pts)
    prev = pts[i - 1]
    cur = pts[i]
    nxt = pts[(i + 1) % n]

    # Strictly redundant point: the shortcut covers exactly the same set of
    # points as the two original segments, so nothing can change.
    if orient(prev, cur, nxt) == 0 and on_segment(prev, nxt, cur):
        return True

    if ch.closed:
        before = _signed_area2(pts)
        after = _signed_area2(pts[:i] + pts[i + 1:])
        sb, sa = sign(before), sign(after)
        if sb != 0 and sa != sb:
            return False
        if sb == 0 and sa != 0:
            return False

    # The swept triangle must contain no vertex of the whole arrangement
    # (on its boundary or in its interior).
    for other in chains:
        for v in other.points:
            if v == prev or v == cur or v == nxt:
                continue
            if point_in_closed_triangle(v, prev, cur, nxt):
                return False

    # The shortcut must not touch or cross any retained segment.
    removed = {frozenset((prev, cur)), frozenset((cur, nxt))}
    for other in chains:
        for (c, d) in _chain_segments(other):
            if frozenset((c, d)) in removed:
                continue
            shared = (c == prev or c == nxt) + (d == prev or d == nxt)
            if shared == 0:
                if segments_intersect(prev, nxt, c, d):
                    return False
            elif shared == 1:
                s, e = (c, d) if (c == prev or c == nxt) else (d, c)
                other_end = nxt if s == prev else prev
                if orient(prev, nxt, e) == 0:
                    # Collinear with the shortcut: only touching exactly at
                    # the shared endpoint is acceptable.
                    dot = ((e[0] - s[0]) * (other_end[0] - s[0])
                           + (e[1] - s[1]) * (other_end[1] - s[1]))
                    if dot > 0:
                        return False
            else:
                # An identical chord already exists elsewhere: adding a
                # second copy would create an overlapping segment.
                return False
    return True


def _simplify_chain(ch, chains, threshold2, removed):
    """Greedily remove the lowest-error topologically safe vertex."""
    while True:
        pts = ch.points
        n = len(pts)
        if ch.closed:
            if n <= 3:  # a ring keeps at least 3 vertices
                return
            candidates = range(n)
        else:
            candidates = range(1, n - 1)
        best_i = -1
        best_e = None
        for i in candidates:
            e = point_segment_dist2(pts[i], pts[i - 1], pts[(i + 1) % n])
            if e > threshold2:
                continue
            if best_e is not None and e >= best_e:
                continue
            if _removal_ok(ch, i, chains):
                best_i, best_e = i, e
        if best_i < 0:
            return
        removed.append((ch.cid, pts[best_i]))
        del pts[best_i]


def simplify_polylines(polylines, threshold=0, closed=None):
    """Simplify polylines under a Hausdorff-style vertex error threshold.

    ``polylines``: sequence of point sequences; a point is any (x, y) pair
    of ints, Fractions or strings accepted by ``Fraction``.
    ``threshold``: non-negative int/Fraction; 0 removes only strictly
    redundant points.  ``closed``: optional list of bools marking rings
    (rings are given without a duplicated closing vertex; a duplicated one
    is detected and dropped).
    """
    t = Fraction(threshold)
    if t < 0:
        raise ValueError("threshold must be >= 0")
    threshold2 = t * t

    norm = [[_pt(p) for p in poly] for poly in polylines]
    if closed is None:
        closed = [False] * len(norm)
    closed = list(closed)
    if len(closed) != len(norm):
        raise ValueError("closed must match polylines in length")
    for pts, cl in zip(norm, closed):
        if cl:
            while len(pts) > 1 and pts[0] == pts[-1]:
                pts.pop()
    ring_starts = [pts[0] if (cl and pts) else None
                   for pts, cl in zip(norm, closed)]

    chains, refs_per_poly = _build_chains(norm, closed)

    removed_raw = []
    for ch in chains:
        _simplify_chain(ch, chains, threshold2, removed_raw)

    out = []
    for refs, cl, start in zip(refs_per_poly, closed, ring_starts):
        pts = []
        for ch, rev in refs:
            seq = ch.points[::-1] if rev else ch.points
            if pts and pts[-1] == seq[0]:
                pts.extend(seq[1:])
            else:
                pts.extend(seq)
        if cl:
            while len(pts) > 1 and pts[0] == pts[-1]:
                pts.pop()
            if start in pts:
                k = pts.index(start)
                pts = pts[k:] + pts[:k]
        out.append(pts)

    # Certified a-posteriori error: exact squared distance of every removed
    # point to the final geometry of its own chain.
    removed = []
    max_e = Fraction(0)
    for cid, p in removed_raw:
        ch = chains[cid]
        segs = _chain_segments(ch)
        if segs:
            d2 = min(point_segment_dist2(p, a, b) for a, b in segs)
        else:
            d2 = min(dist2(p, q) for q in ch.points)
        removed.append(RemovedPoint(p, cid, d2))
        if d2 > max_e:
            max_e = d2
    return SimplifyResult(out, closed, removed, max_e, threshold2)
