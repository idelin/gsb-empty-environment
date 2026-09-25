"""Reachability and cycle-evidence analysis on immutable snapshots."""

from __future__ import annotations

from collections import deque
from typing import Dict, FrozenSet, List, Optional, Tuple

from .graph_data import SnapshotLike
from .results import CycleEvidence


def outgoing_map(snapshot: SnapshotLike) -> Dict[str, list]:
    """Map vertex -> outgoing edges sorted by edge id (deterministic)."""
    out: Dict[str, list] = {v: [] for v in snapshot.vertices}
    for e in snapshot.edges:
        out.setdefault(e.src, []).append(e)
    for edges in out.values():
        edges.sort(key=lambda e: e.eid)
    return out


def incoming_map(snapshot: SnapshotLike) -> Dict[str, list]:
    inc: Dict[str, list] = {v: [] for v in snapshot.vertices}
    for e in snapshot.edges:
        inc.setdefault(e.dst, []).append(e)
    for edges in inc.values():
        edges.sort(key=lambda e: e.eid)
    return inc


def forward_reachable(snapshot: SnapshotLike, source: str) -> set:
    out = outgoing_map(snapshot)
    seen = set()
    if source not in snapshot.vertices:
        return seen
    seen.add(source)
    dq = deque([source])
    while dq:
        u = dq.popleft()
        for e in out.get(u, ()):
            if e.dst not in seen:
                seen.add(e.dst)
                dq.append(e.dst)
    return seen


def can_reach(snapshot: SnapshotLike, target: str) -> set:
    """Vertices from which the target is reachable (reverse BFS)."""
    inc = incoming_map(snapshot)
    seen = set()
    if target not in snapshot.vertices:
        return seen
    seen.add(target)
    dq = deque([target])
    while dq:
        u = dq.popleft()
        for e in inc.get(u, ()):
            if e.src not in seen:
                seen.add(e.src)
                dq.append(e.src)
    return seen


def relevant_slice(snapshot: SnapshotLike, source: str, target: str) -> FrozenSet[str]:
    """Vertices lying on some source->target walk (the reachable slice)."""
    return frozenset(
        forward_reachable(snapshot, source) & can_reach(snapshot, target)
    )


def _bfs_path(snapshot: SnapshotLike, start: str, goal: str) -> Tuple[str, ...]:
    """Deterministic (smallest-edge-id-first) BFS path as edge ids."""
    if start == goal:
        return ()
    out = outgoing_map(snapshot)
    parent = {start: None}
    dq = deque([start])
    while dq:
        u = dq.popleft()
        for e in out.get(u, ()):
            if e.dst in parent:
                continue
            parent[e.dst] = (u, e.eid)
            if e.dst == goal:
                path: List[str] = []
                node = goal
                while parent[node] is not None:
                    prev, eid = parent[node]
                    path.append(eid)
                    node = prev
                path.reverse()
                return tuple(path)
            dq.append(e.dst)
    raise ValueError(f"no path from {start!r} to {goal!r}")


def _find_cycle(snapshot: SnapshotLike, region: FrozenSet[str]):
    """Find (vertex, cycle edge ids) inside the induced region, or None."""
    out = outgoing_map(snapshot)
    # Self-loops first: cheapest and most common witness.
    for v in sorted(region):
        for e in out.get(v, ()):
            if e.dst == v:
                return v, (e.eid,)
    # Otherwise iterative 3-color DFS for a back edge.
    color = {v: 0 for v in region}
    parent: Dict[str, tuple] = {}
    for start in sorted(region):
        if color[start]:
            continue
        color[start] = 1
        stack = [(start, iter(out.get(start, ())))]
        while stack:
            u, it = stack[-1]
            descended = False
            for e in it:
                w = e.dst
                if w not in region:
                    continue
                if color[w] == 0:
                    color[w] = 1
                    parent[w] = (u, e.eid)
                    stack.append((w, iter(out.get(w, ()))))
                    descended = True
                    break
                if color[w] == 1:
                    # Back edge u -> w: cycle is w ..-> u -> w.
                    up_edges: List[str] = []
                    node = u
                    while node != w:
                        prev, eid = parent[node]
                        up_edges.append(eid)
                        node = prev
                    up_edges.reverse()
                    return w, tuple(up_edges) + (e.eid,)
            if not descended:
                color[u] = 2
                stack.pop()
    return None


def find_cycle_evidence(
    snapshot: SnapshotLike, source: str, target: str
) -> Optional[CycleEvidence]:
    """Cycle witness usable for infinitely many source->target walks.

    Only cycles inside the relevant slice count: a cycle that cannot be
    reached from the source, or that cannot lead to the target, does not
    make the walk set infinite.
    """
    region = relevant_slice(snapshot, source, target)
    if not region:
        return None
    found = _find_cycle(snapshot, region)
    if found is None:
        return None
    vertex, cycle = found
    return CycleEvidence(
        cycle_vertex=vertex,
        reach_path=_bfs_path(snapshot, source, vertex),
        cycle=cycle,
        exit_path=_bfs_path(snapshot, vertex, target),
    )
