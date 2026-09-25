"""Constraint-based multi-source log merger.

Three kinds of order constraints participate jointly in feasibility solving:

- local: events from the same source are ordered by their local sequence.
- causal: explicit cross-source happens-before edges.
- time: implied edges from non-overlapping uncertainty intervals
  (u must precede v when hi(u) < lo(v) on the reference timeline).

Contradictions are reported as constraint cycles or as infeasible time
chains; timestamps are never modified to force consistency.
"""

from __future__ import annotations

import heapq
from dataclasses import dataclass

from .events import Event
from .offsets import OffsetTable
from .timevals import Interval, TimePoint

LOCAL = "local"
CAUSAL = "causal"
TIME = "time"


@dataclass(frozen=True)
class Edge:
    src: str
    dst: str
    kind: str


@dataclass(frozen=True)
class MarkedEvent:
    event: Event
    interval: Interval
    next_necessary: object


@dataclass(frozen=True)
class MergeResult:
    order: tuple
    edges: tuple
    necessary: frozenset

    @property
    def ids(self) -> tuple:
        return tuple(m.event.id for m in self.order)


@dataclass(frozen=True)
class Contradiction:
    reason: str
    cycle: tuple
    chain: tuple
    message: str


def _intervals(events, offsets: OffsetTable) -> dict:
    out = {}
    for e in events:
        ref = offsets.to_reference(e.clock)
        out[e.id] = Interval(
            TimePoint(ref.value - e.error.value),
            TimePoint(ref.value + e.error.value),
        )
    return out


def _build_edges(events, intervals) -> list:
    edges = []
    by_source = {}
    for e in events:
        by_source.setdefault(e.source, []).append(e)
    for group in by_source.values():
        group.sort(key=lambda e: (e.seq, e.id))
        for a, b in zip(group, group[1:]):
            edges.append(Edge(a.id, b.id, LOCAL))
    ids = {e.id for e in events}
    for e in events:
        for dep in e.causal_after:
            if dep not in ids:
                raise KeyError(f"unknown causal dependency {dep!r} for {e.id!r}")
            edges.append(Edge(dep, e.id, CAUSAL))
    id_list = [e.id for e in events]
    for i, a in enumerate(id_list):
        for b in id_list[i + 1:]:
            if intervals[a].definitely_before(intervals[b]):
                edges.append(Edge(a, b, TIME))
            elif intervals[b].definitely_before(intervals[a]):
                edges.append(Edge(b, a, TIME))
    return edges


def _adjacency(ids, edges):
    adj = {i: [] for i in ids}
    for e in edges:
        adj[e.src].append(e)
    return adj


def _find_cycle(ids, edges):
    adj = _adjacency(ids, edges)
    for e in edges:
        if e.src == e.dst:
            return [e]
    color = {i: 0 for i in ids}
    path = []

    def dfs(u):
        color[u] = 1
        path.append(u)
        for e in adj[u]:
            if color[e.dst] == 1:
                idx = path.index(e.dst)
                nodes = path[idx:] + [e.dst]
                cyc = []
                for a, b in zip(nodes, nodes[1:]):
                    kind = next(x.kind for x in adj[a] if x.dst == b)
                    cyc.append(Edge(a, b, kind))
                return cyc
            if color[e.dst] == 0:
                found = dfs(e.dst)
                if found is not None:
                    return found
        path.pop()
        color[u] = 2
        return None

    for i in ids:
        if color[i] == 0:
            found = dfs(i)
            if found is not None:
                return found
    return None


def _topo_sort(ids, edges, key):
    adj = {i: set() for i in ids}
    indeg = {i: 0 for i in ids}
    for e in edges:
        if e.dst not in adj[e.src]:
            adj[e.src].add(e.dst)
            indeg[e.dst] += 1
    heap = [(key(i), i) for i in ids if indeg[i] == 0]
    heapq.heapify(heap)
    order = []
    while heap:
        _, u = heapq.heappop(heap)
        order.append(u)
        for v in adj[u]:
            indeg[v] -= 1
            if indeg[v] == 0:
                heapq.heappush(heap, (key(v), v))
    return order


def _check_time_feasible(ids, intervals, edges):
    """Exact feasibility of strict time constraints over intervals.

    Every explicit (local/causal) edge u->v requires t_u < t_v with
    t_x in interval(x). Lower bounds are propagated as (value, strict)
    pairs; a node is infeasible when its forced lower bound exceeds its
    interval upper bound. Returns a witness chain of event ids, or None.
    """
    explicit = [e for e in edges if e.kind != TIME]
    topo = _topo_sort(ids, explicit, key=lambda i: (intervals[i].lo.value, i))
    preds = {i: [] for i in ids}
    for e in explicit:
        preds[e.dst].append(e.src)
    lower = {}
    parent = {}
    for v in topo:
        best = (intervals[v].lo.value, False)
        src = None
        for u in preds[v]:
            cand = (lower[u][0], True)
            if cand > best:
                best, src = cand, u
        lower[v] = best
        parent[v] = src
    for v in ids:
        if lower[v] > (intervals[v].hi.value, False):
            chain = []
            cur = v
            while cur is not None:
                chain.append(cur)
                cur = parent[cur]
            chain.reverse()
            return chain
    return None


def _closure(ids, topo, edges):
    adj = {i: set() for i in ids}
    for e in edges:
        adj[e.src].add(e.dst)
    bit = {i: 1 << n for n, i in enumerate(ids)}
    reach = {}
    for u in reversed(topo):
        r = 0
        for v in adj[u]:
            r |= bit[v] | reach[v]
        reach[u] = r
    return bit, reach


class Merger:
    def __init__(self, offsets: OffsetTable):
        self._offsets = offsets

    def merge(self, events):
        events = sorted(events, key=lambda e: e.id)
        ids = [e.id for e in events]
        if len(set(ids)) != len(ids):
            raise ValueError("duplicate event ids passed to merge")
        intervals = _intervals(events, self._offsets)
        edges = _build_edges(events, intervals)

        cycle = _find_cycle(ids, edges)
        if cycle is not None:
            desc = " -> ".join(f"{e.src}[{e.kind}]" for e in cycle) + f" -> {cycle[-1].dst}"
            return Contradiction(
                reason="cycle",
                cycle=tuple(cycle),
                chain=(),
                message=f"constraint cycle detected: {desc}",
            )

        chain = _check_time_feasible(ids, intervals, edges)
        if chain is not None:
            return Contradiction(
                reason="infeasible-time",
                cycle=(),
                chain=tuple(chain),
                message=(
                    "no timestamp assignment satisfies the constraints along "
                    + " -> ".join(chain)
                    + "; timestamps are not modified"
                ),
            )

        topo = _topo_sort(ids, edges, key=lambda i: (intervals[i].lo.value, i))
        bit, reach = _closure(ids, topo, edges)
        necessary = frozenset(
            (u, ids[j]) for u in ids for j in range(len(ids)) if reach[u] & (1 << j)
        )
        by_id = {e.id: e for e in events}
        marked = []
        for pos, eid in enumerate(topo):
            if pos + 1 < len(topo):
                nxt = topo[pos + 1]
                flag = bool(reach[eid] & bit[nxt])
            else:
                flag = None
            marked.append(MarkedEvent(by_id[eid], intervals[eid], flag))
        return MergeResult(tuple(marked), tuple(edges), necessary)


def enumerate_extensions(ids, edges, limit=50000):
    """Brute-force enumeration of all linear extensions (small inputs)."""
    adj = {i: set() for i in ids}
    for e in edges:
        adj[e.src].add(e.dst)
    indeg = {i: 0 for i in ids}
    for u in adj:
        for v in adj[u]:
            indeg[v] += 1
    avail = sorted(i for i in ids if indeg[i] == 0)
    out = []
    order = []
    truncated = [False]

    def backtrack():
        if truncated[0]:
            return
        if len(order) == len(ids):
            out.append(tuple(order))
            if len(out) >= limit:
                truncated[0] = True
            return
        for v in list(avail):
            avail.remove(v)
            order.append(v)
            added = []
            for w in adj[v]:
                indeg[w] -= 1
                if indeg[w] == 0:
                    avail.append(w)
                    added.append(w)
            backtrack()
            for w in adj[v]:
                indeg[w] += 1
            for w in added:
                avail.remove(w)
            order.pop()
            avail.append(v)

    backtrack()
    return out, not truncated[0]


def verify_necessity(ids, edges, necessary, limit=50000):
    """Cross-check closure-derived necessary pairs against full enumeration."""
    exts, complete = enumerate_extensions(ids, edges, limit)
    if not complete:
        return ["enumeration truncated; increase limit"]
    problems = []
    for a in ids:
        for b in ids:
            if a == b:
                continue
            always = all(x.index(a) < x.index(b) for x in exts)
            expected = (a, b) in necessary
            if always != expected:
                problems.append(
                    f"pair ({a}, {b}): closure says necessary={expected}, "
                    f"enumeration says necessary={always}"
                )
    return problems
