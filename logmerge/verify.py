"""Small-scale independent verification of the merge output.

Two cross-checks, both meant for small inputs:

* ``verify_markers`` enumerates ALL linear extensions of the
  necessary-order DAG and confirms that (a) every pair marked necessary
  keeps that order in every extension and (b) every pair marked
  uncertain appears in both orders across the extension set.

* ``sampled_orders`` enumerates offset assignments on a per-source grid
  (lo / mid / hi of each interval), keeps only assignments that satisfy
  every local-order and causal constraint, and collects the distinct
  total orders they induce. Every sampled order must be a linear
  extension of the necessary-order relation.
"""
from __future__ import annotations

from fractions import Fraction
from itertools import product

from .events import Event
from .merger import MergeResult
from .timemodel import OffsetTable


def linear_extensions(nodes, edges, limit: int = 100_000):
    """All topological orders of (nodes, edges); None if more than `limit`."""
    nodes = list(nodes)
    succ = {n: [] for n in nodes}
    indeg = {n: 0 for n in nodes}
    for a, b in edges:
        succ[a].append(b)
        indeg[b] += 1
    avail = [n for n in nodes if indeg[n] == 0]
    out: list[tuple] = []
    cur: list = []
    overflow = False

    def backtrack():
        nonlocal overflow
        if overflow:
            return
        if not avail:
            out.append(tuple(cur))
            if len(out) > limit:
                overflow = True
            return
        for n in list(avail):
            avail.remove(n)
            cur.append(n)
            pushed = []
            for m in succ[n]:
                indeg[m] -= 1
                if indeg[m] == 0:
                    avail.append(m)
                    pushed.append(m)
            backtrack()
            for m in pushed:
                avail.remove(m)
            for m in succ[n]:
                indeg[m] += 1
            cur.pop()
            avail.append(n)

    backtrack()
    return None if overflow else out


def verify_markers(result: MergeResult, limit: int = 100_000) -> list[str]:
    """Return a list of problems; empty means the markers check out."""
    extensions = linear_extensions(result.order, result.necessary_pairs, limit)
    if extensions is None:
        return [f"skipped: more than {limit} linear extensions"]
    problems: list[str] = []
    for a, b in result.necessary_pairs:
        for ext in extensions:
            if ext.index(a) > ext.index(b):
                problems.append(f"necessary pair ({a}, {b}) violated in {ext}")
                break
    for a, b in result.uncertain_pairs:
        seen_ab = seen_ba = False
        for ext in extensions:
            if ext.index(a) < ext.index(b):
                seen_ab = True
            else:
                seen_ba = True
            if seen_ab and seen_ba:
                break
        if not (seen_ab and seen_ba):
            problems.append(f"uncertain pair ({a}, {b}) never varies")
    return problems


def sampled_orders(
    events: tuple[Event, ...] | list[Event],
    table: OffsetTable,
    max_sources: int = 4,
) -> set[tuple[str, ...]] | None:
    """Distinct strict total orders induced by grid-sampled offsets.

    Returns None when there are too many sources for an exhaustive grid.
    """
    events = list(events)
    sources = sorted({e.source for e in events})
    if len(sources) > max_sources:
        return None
    grid = []
    for s in sources:
        iv = table.interval(s)
        mid = (iv.lo.amount + iv.hi.amount) / 2
        grid.append(sorted({iv.lo.amount, mid, iv.hi.amount}))
    local_edges = []
    per_source: dict[str, list[str]] = {}
    for e in events:
        per_source.setdefault(e.source, []).append(e.id)
    for seq in per_source.values():
        local_edges.extend(zip(seq, seq[1:]))
    causal_edges = [(c, e.id) for e in events for c in e.causes]
    by_id = {e.id: e for e in events}

    orders: set[tuple[str, ...]] = set()
    for combo in product(*grid):
        offsets = dict(zip(sources, combo))
        times = {e.id: e.wall.reading + offsets[e.source] for e in events}
        if any(times[a] > times[b] for a, b in local_edges + causal_edges):
            continue  # assignment violates local order or causality
        if len(set(times.values())) != len(times):
            continue  # ties leave order ambiguous; skip
        orders.add(tuple(sorted(times, key=times.get)))
    return orders
