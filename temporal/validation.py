"""Independent whole-graph difference-constraints checker.

Deliberately implemented separately from ``TemporalNetwork`` (plain
Bellman-Ford over all active constraints, no incremental state) so random
incremental updates can be cross-validated against a full recompute.
"""

from __future__ import annotations

from fractions import Fraction

from .network import ANCHOR


def reference_solve(event_names, constraints):
    """Solve the whole system from scratch.

    Returns a dict with keys:
      - ``feasible``: bool
      - ``pot``:      a feasible potential/witness assignment (if feasible)
      - ``bounds``:   {event: (earliest, latest)} with None for unbounded
    """
    vertices = [ANCHOR, *event_names]
    edges = []
    for c in constraints:
        if c.upper is not None:
            edges.append((c.source, c.target, Fraction(c.upper), c.cid))
        if c.lower is not None:
            edges.append((c.target, c.source, -Fraction(c.lower), c.cid))

    n = len(vertices)
    pot = {v: Fraction(0) for v in vertices}
    feasible = True
    for i in range(n):
        changed = False
        for (u, v, w, _cid) in edges:
            if pot[v] > pot[u] + w:
                pot[v] = pot[u] + w
                changed = True
                if i == n - 1:
                    feasible = False
        if not changed:
            break

    result = {"feasible": feasible, "pot": pot}
    if not feasible:
        return result

    latest = _shortest_from_anchor(edges, n)
    reversed_edges = [(v, u, w, cid) for (u, v, w, cid) in edges]
    to_anchor = _shortest_from_anchor(reversed_edges, n)

    bounds = {}
    for e in event_names:
        lo = -to_anchor[e] if e in to_anchor else None
        hi = latest.get(e)
        bounds[e] = (lo, hi)
    result["bounds"] = bounds
    return result


def _shortest_from_anchor(edges, n):
    dist = {ANCHOR: Fraction(0)}
    for _ in range(n - 1):
        changed = False
        for (u, v, w, _cid) in edges:
            if u in dist:
                cand = dist[u] + w
                if v not in dist or cand < dist[v]:
                    dist[v] = cand
                    changed = True
        if not changed:
            break
    return dist
