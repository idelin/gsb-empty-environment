"""Merge multi-source logs into a stable causal linear extension.

Order semantics
---------------
An event ``a`` is *necessarily before* ``b`` iff every feasible
explanation of the data orders them that way, i.e.

* there is a path of explicit causal edges and/or same-source log-order
  steps from ``a`` to ``b`` (causality wins over wall-clock readings --
  a reading that looks "reversed" is explained by clock offset), or
* the joint constraint system (offsets + local order + causality)
  entails ``t_a < t_b`` strictly.

Pairs that are comparable neither way are *uncertain*: their error
intervals overlap and no causality ties them, so either order is
consistent with the data. The output is one deterministic (stable)
linear extension, with every adjacent pair explicitly marked as
necessary or uncertain.
"""
from __future__ import annotations

import heapq
from dataclasses import dataclass
from itertools import combinations, permutations

from .constraints import Constraint, DifferenceConstraints
from .events import Event, dedupe
from .timemodel import OffsetTable, TimePoint

ZERO = "@zero"


class CausalCycleError(Exception):
    """The explicit happens-before graph (plus local order) contains a cycle."""

    def __init__(self, cycle: list[str]):
        self.cycle = list(cycle)
        super().__init__("causal cycle: " + " -> ".join(self.cycle))


class InfeasibleError(Exception):
    """Offsets, local order and causality contradict each other.

    Carries the concrete ring of contradictory constraints. The merger
    refuses to "repair" the data by editing recorded times.
    """

    def __init__(self, ring: list[Constraint]):
        self.ring = list(ring)
        detail = "\n".join(f"  {c}" for c in self.ring)
        super().__init__(
            "contradictory constraints (times left unmodified):\n" + detail
        )


@dataclass(frozen=True, slots=True)
class AdjacentMark:
    """One adjacent pair of the emitted order."""

    before: str
    after: str
    necessary: bool


@dataclass(frozen=True)
class MergeResult:
    order: tuple[str, ...]
    adjacent: tuple[AdjacentMark, ...]
    necessary_pairs: frozenset[tuple[str, str]]  # (a, b) means a necessarily precedes b
    uncertain_pairs: frozenset[tuple[str, str]]  # sorted id tuples, either order possible
    time_bounds: dict[str, tuple[TimePoint, TimePoint]]
    events: tuple[Event, ...]

    def position(self, event_id: str) -> int:
        return self.order.index(event_id)


def _tvar(event_id: str) -> str:
    return f"t:{event_id}"


def _ovar(source: str) -> str:
    return f"o:{source}"


def _find_directed_cycle(nodes: list[str], succ: dict[str, list[str]]) -> list[str] | None:
    WHITE, GRAY, BLACK = 0, 1, 2
    color = {n: WHITE for n in nodes}
    stack: list[str] = []

    def dfs(u: str) -> list[str] | None:
        color[u] = GRAY
        stack.append(u)
        for v in succ.get(u, ()):
            if color[v] == GRAY:
                return stack[stack.index(v):] + [v]
            if color[v] == WHITE:
                found = dfs(v)
                if found is not None:
                    return found
        stack.pop()
        color[u] = BLACK
        return None

    for n in nodes:
        if color[n] == WHITE:
            found = dfs(n)
            if found is not None:
                return found
    return None


def merge(events, table: OffsetTable) -> MergeResult:
    events = tuple(dedupe(events))
    by_id = {e.id: e for e in events}
    ids = [e.id for e in events]

    for e in events:
        table.interval(e.source)  # raises on unregistered source
        if e.wall.source and e.wall.source != e.source:
            raise ValueError(
                f"event {e.id!r}: wall clock source {e.wall.source!r} "
                f"!= event source {e.source!r}"
            )
        for c in e.causes:
            if c not in by_id:
                raise ValueError(f"event {e.id!r} causes unknown event {c!r}")

    # Local order: per-source input sequence.
    per_source: dict[str, list[str]] = {}
    for e in events:
        per_source.setdefault(e.source, []).append(e.id)
    local_edges: list[tuple[str, str]] = []
    for seq in per_source.values():
        local_edges.extend(zip(seq, seq[1:]))

    causal_edges: list[tuple[str, str]] = []
    for e in events:
        causal_edges.extend((c, e.id) for c in e.causes)

    # 1. Causal cycle check (a zero-weight cycle is time-feasible but
    #    causally impossible, so it needs its own detector).
    succ: dict[str, list[str]] = {}
    for a, b in causal_edges + local_edges:
        succ.setdefault(a, []).append(b)
    cycle = _find_directed_cycle(ids, succ)
    if cycle is not None:
        raise CausalCycleError(cycle)

    # 2. Joint feasibility: offsets + local order + causality.
    system = DifferenceConstraints()
    for source in per_source:
        iv = table.interval(source)
        system.add(_ovar(source), ZERO, iv.hi.amount,
                   Constraint("offset", f"offset({source}) <= {iv.hi.amount}"))
        system.add(ZERO, _ovar(source), -iv.lo.amount,
                   Constraint("offset", f"offset({source}) >= {iv.lo.amount}"))
    for e in events:
        system.add_equal(
            _tvar(e.id), _ovar(e.source), e.wall.reading,
            Constraint("wall", f"t({e.id}) = {e.wall.reading} + offset({e.source})"),
        )
    for a, b in local_edges:
        system.add(_tvar(a), _tvar(b), 0,
                   Constraint("local", f"{a} precedes {b} in source log order"))
    for a, b in causal_edges:
        system.add(_tvar(a), _tvar(b), 0,
                   Constraint("causal", f"{a} happens-before {b}"))
    ring = system.find_contradiction()
    if ring is not None:
        raise InfeasibleError(ring)
    bounds = system.closure()

    # 3. Reachability in the causal/local graph.
    reach: dict[str, set[str]] = {i: set() for i in ids}
    for a, bs in succ.items():
        reach[a].update(bs)
    changed = True
    while changed:
        changed = False
        for a in ids:
            grew = set().union(*(reach[b] for b in reach[a])) if reach[a] else set()
            if not grew <= reach[a]:
                reach[a] |= grew
                changed = True

    # 4. Necessary-order relation.
    necessary: set[tuple[str, str]] = set()
    for a, b in permutations(ids, 2):
        if b in reach[a]:
            necessary.add((a, b))
        else:
            ub = bounds.ub_diff(_tvar(a), _tvar(b))
            if ub is not None and ub < 0:
                necessary.add((a, b))
    uncertain = {
        tuple(sorted((a, b)))
        for a, b in combinations(ids, 2)
        if (a, b) not in necessary and (b, a) not in necessary
    }

    # 5. Stable linear extension: Kahn with a deterministic priority key
    #    (earliest feasible true time, then source, wall reading, id).
    earliest = {i: bounds.lb(_tvar(i), ZERO) for i in ids}
    key = {}
    for e in events:
        key[e.id] = (earliest[e.id], e.source, e.wall.reading, e.id)
    indeg = {i: 0 for i in ids}
    for a, b in necessary:
        indeg[b] += 1
    heap = [(key[i], i) for i in ids if indeg[i] == 0]
    heapq.heapify(heap)
    order: list[str] = []
    while heap:
        _, u = heapq.heappop(heap)
        order.append(u)
        for v in ids:
            if (u, v) in necessary:
                indeg[v] -= 1
                if indeg[v] == 0:
                    heapq.heappush(heap, (key[v], v))

    adjacent = tuple(
        AdjacentMark(a, b, (a, b) in necessary)
        for a, b in zip(order, order[1:])
    )
    time_bounds = {
        i: (TimePoint(bounds.lb(_tvar(i), ZERO)), TimePoint(bounds.ub(_tvar(i), ZERO)))
        for i in ids
    }
    return MergeResult(
        order=tuple(order),
        adjacent=adjacent,
        necessary_pairs=frozenset(necessary),
        uncertain_pairs=frozenset(uncertain),
        time_bounds=time_bounds,
        events=events,
    )
