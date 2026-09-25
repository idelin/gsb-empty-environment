"""Topology-preserving simplification of planar polyline networks.

Given a collection of polylines / polygons that may share boundaries, this
module simplifies every polyline within a distance tolerance while
guaranteeing:

* **No new crossings** - a shortcut chord is accepted only if it does not
  cross, touch (except at its own endpoints) or overlap any current edge of
  the whole network.  Narrow channels therefore stay open.
* **No flipped or collapsed rings** - closed rings keep at least 3 vertices
  and keep their orientation (verified with exact signed area).
* **Identical shared boundaries** - coincident edges of different input
  polylines are unified into a single graph chain and simplified exactly
  once, so two regions sharing an edge keep byte-identical boundaries.
* **Error bounds** - every removed original vertex is reported together with
  its exact squared distance to the simplified polyline; by construction
  this never exceeds ``tol**2``.

All arithmetic is exact (``fractions.Fraction``); there is no epsilon
anywhere.  ``tol`` itself must be an ``int`` or ``Fraction``.

Degenerate cases (defined behaviour)
------------------------------------
* Empty polyline            -> returned unchanged (empty).
* Single point              -> returned unchanged.
* Consecutive duplicates    -> dropped as strictly redundant (error 0).
* Ring with <2 distinct pts -> collapses to a single point.
* Zero-area ring            -> may lose collinear vertices, never flips
                               (orientation is undefined for area 0).
* ``tol == 0``              -> only strictly redundant vertices are removed
                               (points exactly on the segment between their
                               kept neighbours, subject to topology checks).
* Self-intersecting input   -> intersections become junction vertices; the
                               output never adds *new* crossings.
"""

from dataclasses import dataclass
from fractions import Fraction
from math import sqrt

from geom import (
    NONE,
    TOUCH,
    coord as _coord,
    dist2_point_segment,
    point as _point,
    segments_relation,
    twice_signed_area,
)

__all__ = ["RemovedPoint", "SimplifyResult", "simplify_network"]


@dataclass
class RemovedPoint:
    """An original vertex that was removed, with its exact error bound."""

    polyline: int          # index of the input polyline
    index: int             # index of the vertex in that input polyline
    point: tuple           # exact (Fraction, Fraction) coordinates
    error_sq: Fraction     # exact squared distance to the simplified polyline

    @property
    def error(self) -> float:
        """Distance to the simplified polyline (float approximation)."""
        return sqrt(float(self.error_sq))


@dataclass
class SimplifyResult:
    polylines: list        # simplified polylines, same order/structure as input
    removed: list          # list[RemovedPoint] for every removed vertex
    max_error_sq: Fraction # exact maximum squared error over all removed points

    @property
    def max_error(self) -> float:
        return sqrt(float(self.max_error_sq))


def _key(u, v):
    """Undirected edge key."""
    return (u, v) if u <= v else (v, u)


def _dedupe_consecutive(pts, cyclic):
    out = []
    for p in pts:
        if not out or out[-1] != p:
            out.append(p)
    if cyclic:
        while len(out) > 1 and out[0] == out[-1]:
            out.pop()
    return out


class _Chain:
    """A maximal run of degree-2 graph vertices, simplified as one polyline."""

    __slots__ = ("pts", "ring", "spans", "kept")

    def __init__(self, pts, ring):
        self.pts = pts  # linearized vertices; for rings pts[-1] == pts[0]
        self.ring = ring
        self.spans = [(i, i + 1) for i in range(len(pts) - 1)]
        self.kept = None


def simplify_network(polylines, tol=0):
    """Simplify a network of polylines/polygons with topology preservation.

    ``polylines`` is a list of sequences of ``(x, y)`` pairs (int or
    Fraction).  A polyline whose first and last points coincide is treated
    as a closed ring.  ``tol`` is the distance tolerance (int or Fraction,
    >= 0).  Returns a :class:`SimplifyResult`.
    """
    tol = _coord(tol)
    if tol < 0:
        raise ValueError("tolerance must be non-negative")
    tol2 = tol * tol

    # ---- 1. Normalize input ------------------------------------------------
    originals = []  # points exactly as given (for error reporting)
    polys = []      # working copy: closure removed, consecutive dups dropped
    rings = []
    for poly in polylines:
        pts = [_point(p) for p in poly]
        originals.append(pts)
        ring = len(pts) >= 2 and pts[0] == pts[-1]
        body = pts[:-1] if ring else list(pts)
        body = _dedupe_consecutive(body, ring)
        if ring and len(body) < 2:
            ring = False  # degenerate ring collapses to a single point
        rings.append(ring)
        polys.append(body)

    # ---- 2. Collect segments ----------------------------------------------
    seg_list = []
    seg_owner = []
    for pi, pts in enumerate(polys):
        n = len(pts)
        last = n if rings[pi] else n - 1
        for i in range(last):
            a, b = pts[i], pts[(i + 1) % n]
            if a != b:
                seg_list.append((a, b))
                seg_owner.append(pi)

    # ---- 3. Split segments at every intersection / overlap (exact) ---------
    splits = [set() for _ in seg_list]
    for i in range(len(seg_list)):
        a, b = seg_list[i]
        for j in range(i + 1, len(seg_list)):
            c, d = seg_list[j]
            rel = segments_relation(a, b, c, d)
            kind = rel[0]
            if kind == NONE:
                continue
            if kind == "cross":
                splits[i].add(rel[1])
                splits[j].add(rel[1])
            elif kind == "touch":
                for t in rel[1]:
                    if t != a and t != b:
                        splits[i].add(t)
                    if t != c and t != d:
                        splits[j].add(t)
            else:  # overlap
                for t in (rel[1], rel[2]):
                    if t != a and t != b:
                        splits[i].add(t)
                    if t != c and t != d:
                        splits[j].add(t)

    # ---- 4. Atomic edges; per-polyline directed edge sequences -------------
    poly_edges = [[] for _ in polys]
    edge_mult = {}
    for si, (a, b) in enumerate(seg_list):
        mid = sorted(splits[si])
        if b < a:
            mid = mid[::-1]
        seq = [a] + mid + [b]
        for u, v in zip(seq, seq[1:]):
            poly_edges[seg_owner[si]].append((u, v))
            k = _key(u, v)
            edge_mult[k] = edge_mult.get(k, 0) + 1

    # ---- 5. Planar graph: junctions and chains -----------------------------
    adj = {}
    for u, v in edge_mult:
        adj.setdefault(u, set()).add(v)
        adj.setdefault(v, set()).add(u)
    endpoints = set()
    for pi, pts in enumerate(polys):
        if not rings[pi] and pts:
            endpoints.add(pts[0])
            endpoints.add(pts[-1])

    def is_junction(p):
        return len(adj.get(p, ())) != 2 or p in endpoints

    visited = set()
    chains = []
    edge_loc = {}  # undirected edge key -> (chain id, edge index)

    def add_chain(verts, ring):
        cid = len(chains)
        lin = verts + [verts[0]] if ring else verts
        chains.append(_Chain(lin, ring))
        for idx in range(len(lin) - 1):
            k = _key(lin[idx], lin[idx + 1])
            edge_loc[k] = (cid, idx)
            visited.add(k)

    # Open chains between junctions.
    for start in list(adj):
        if not is_junction(start):
            continue
        for nb in adj[start]:
            if _key(start, nb) in visited:
                continue
            verts = [start, nb]
            prev, cur = start, nb
            while not is_junction(cur):
                (nxt,) = [w for w in adj[cur] if w != prev]
                verts.append(nxt)
                prev, cur = cur, nxt
            add_chain(verts, ring=False)

    # Remaining edges form pure rings (no junctions).
    for k in list(edge_mult):
        if k in visited:
            continue
        start = k[0]
        verts = [start]
        prev, cur = None, start
        while True:
            nbs = [w for w in adj[cur] if w != prev]
            nxt = nbs[0]
            if nxt == start:
                break
            verts.append(nxt)
            prev, cur = cur, nxt
        add_chain(verts, ring=True)

    # ---- 6. Topology-preserving Douglas-Peucker per chain ------------------
    current = set(edge_mult)  # current undirected edges of the whole network

    def chord_ok(a, b, excluded):
        """Chord ab must not cross/touch/overlap any current edge."""
        for e in current:
            if e in excluded:
                continue
            rel = segments_relation(a, b, e[0], e[1])
            kind = rel[0]
            if kind == NONE:
                continue
            if kind == TOUCH and all(t == a or t == b for t in rel[1]):
                continue
            return False
        return True

    for chain in chains:
        pts = chain.pts
        m = len(pts) - 1
        if m < 2:
            chain.kept = list(range(m + 1))
            continue

        orig_sign = 0
        if chain.ring:
            area2 = twice_signed_area(pts[:-1])
            orig_sign = (area2 > 0) - (area2 < 0)
            # Split the ring at the vertex farthest from pts[0].
            bidx = max(
                range(1, m),
                key=lambda i: dist2_point_segment(pts[i], pts[0], pts[0]),
            )
            starts = [(0, bidx), (bidx, m)]
        else:
            starts = [(0, m)]

        def guard_ok(i, j, chain=chain, pts=pts, orig_sign=orig_sign):
            # Rings must keep >= 3 vertices and their orientation.
            if not chain.ring or orig_sign == 0:
                return True
            cand = [s for s in chain.spans if s[1] <= i or s[0] >= j]
            cand.append((i, j))
            if len(cand) < 3:
                return False
            verts = [pts[s[0]] for s in sorted(cand)]
            area2 = twice_signed_area(verts)
            return ((area2 > 0) - (area2 < 0)) == orig_sign

        stack = list(starts)
        while stack:
            i, j = stack.pop()
            if j - i < 2:
                continue
            a, b = pts[i], pts[j]
            dmax = Fraction(-1)
            k = i
            for t in range(i + 1, j):
                d2 = dist2_point_segment(pts[t], a, b)
                if d2 > dmax:
                    dmax = d2
                    k = t
            if dmax <= tol2:
                excluded = {_key(pts[t], pts[t + 1]) for t in range(i, j)}
                if chord_ok(a, b, excluded) and guard_ok(i, j):
                    chain.spans = [
                        s for s in chain.spans if s[1] <= i or s[0] >= j
                    ]
                    chain.spans.append((i, j))
                    chain.spans.sort()
                    current.difference_update(excluded)
                    current.add(_key(a, b))
                    continue
            stack.append((i, k))
            stack.append((k, j))
        chain.kept = sorted(
            {s[0] for s in chain.spans} | {s[1] for s in chain.spans}
        )

    # ---- 7. Reconstruct each input polyline from simplified chains ---------
    out_polys = []
    for pi, pts in enumerate(polys):
        edges = poly_edges[pi]
        if not edges:
            out_polys.append(list(pts))
            continue
        grouped = []  # maximal runs of consecutive edges on the same chain
        for u, v in edges:
            cid, idx = edge_loc[_key(u, v)]
            if grouped and grouped[-1][0] == cid:
                grouped[-1][1].append((idx, u))
            else:
                grouped.append((cid, [(idx, u)]))
        out = []
        for cid, items in grouped:
            chain = chains[cid]
            lo = min(i for i, _ in items)
            hi = max(i for i, _ in items)
            fwd = items[0][1] == chain.pts[items[0][0]]
            sel = [k for k in chain.kept if lo <= k <= hi + 1]
            if not sel or sel[0] != lo:
                sel = [lo] + sel
            if sel[-1] != hi + 1:
                sel.append(hi + 1)
            seg_pts = [chain.pts[k] for k in sel]
            if not fwd:
                seg_pts.reverse()
            for p in seg_pts:
                if not out or out[-1] != p:
                    out.append(p)
        if rings[pi] and out and out[0] != out[-1]:
            out.append(out[0])
        out_polys.append(out)

    # ---- 8. Exact error bounds for removed vertices ------------------------
    removed = []
    max_err = Fraction(0)
    for pi, orig in enumerate(originals):
        simp = out_polys[pi]
        if not simp:
            continue
        kept_pts = set(simp)
        segs = list(zip(simp, simp[1:]))
        for idx, p in enumerate(orig):
            if p in kept_pts:
                continue
            if segs:
                d2 = min(dist2_point_segment(p, a, b) for a, b in segs)
            else:
                d2 = dist2_point_segment(p, simp[0], simp[0])
            removed.append(RemovedPoint(pi, idx, p, d2))
            if d2 > max_err:
                max_err = d2

    return SimplifyResult(out_polys, removed, max_err)
