"""Lexicographic k-th walk selection and its inverse ranking.

Walks are ordered by their edge-id sequences, prefix-first (a walk that
is a strict prefix of another sorts earlier).  Selection is driven only
by exact counts; modular counts are never used here, so the two result
kinds (counts and selections) cannot be mixed.
"""

from __future__ import annotations

from .analysis import outgoing_map
from .errors import BudgetExhausted, NoSuchWalk, TrajectoryTooLong
from .results import WalkResult


class _Budget:
    __slots__ = ("remaining",)

    def __init__(self, limit: int):
        self.remaining = limit

    def spend(self, n: int = 1) -> None:
        self.remaining -= n
        if self.remaining < 0:
            raise BudgetExhausted(
                "selection budget exhausted; the answer is unknown, "
                "which is not the same as 'no path exists'"
            )


def _vertices_for(snapshot, edge_ids, start):
    by_id = {e.eid: e for e in snapshot.edges}
    verts = [start]
    u = start
    for eid in edge_ids:
        u = by_id[eid].dst
        verts.append(u)
    return tuple(verts)


def kth_walk(snapshot, engine, source, target, lo, hi, k, *, max_length, budget):
    """Return the k-th (0-based) source->target walk with length in [lo, hi)."""
    if k < 0:
        raise NoSuchWalk(f"negative rank {k}")
    out = outgoing_map(snapshot)
    spend = _Budget(budget).spend
    spend()  # total-count precheck
    total = engine.count_range(source, target, lo, hi)
    if k >= total:
        raise NoSuchWalk(f"rank {k} >= total of {total} walks in [{lo}, {hi})")
    k0 = k
    u = source
    a, b = lo, hi  # remaining lengths allowed: a <= remaining < b
    chosen = []
    while True:
        if u == target and a <= 0:
            # The empty continuation is a complete walk and sorts first.
            if k == 0:
                edges = tuple(chosen)
                return WalkResult(
                    version=snapshot.version,
                    k=k0,
                    edges=edges,
                    vertices=_vertices_for(snapshot, edges, source),
                )
            k -= 1
        advanced = False
        for e in out.get(u, ()):
            spend()
            completions = engine.count_range(e.dst, target, max(a - 1, 0), b - 1)
            if k < completions:
                chosen.append(e.eid)
                u = e.dst
                a = max(a - 1, 0)
                b = b - 1
                advanced = True
                break
            k -= completions
        if not advanced:
            # Unreachable when k < total; kept as a safety net.
            raise NoSuchWalk("selection exhausted all candidates")
        if len(chosen) > max_length:
            raise TrajectoryTooLong(
                f"walk exceeds max_length={max_length}; refusing to "
                "materialize an unbounded trajectory"
            )


def rank_walk(snapshot, engine, source, target, lo, hi, edge_ids):
    """Inverse of kth_walk: lexicographic rank of a concrete walk."""
    out = outgoing_map(snapshot)
    u = source
    a, b = lo, hi
    rank = 0
    for eid in edge_ids:
        if u == target and a <= 0:
            rank += 1  # the walk that stops here sorts before any extension
        nxt = None
        for e in out.get(u, ()):
            if e.eid == eid:
                nxt = e
                break
            rank += engine.count_range(e.dst, target, max(a - 1, 0), b - 1)
        if nxt is None:
            raise ValueError(
                f"edge {eid!r} does not leave vertex {u!r} "
                f"in version {snapshot.version}"
            )
        u = nxt.dst
        a = max(a - 1, 0)
        b = b - 1
    if u != target or a > 0:
        raise ValueError("edge sequence is not a complete walk inside the interval")
    return rank
