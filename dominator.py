"""Immediate dominators over a directed multigraph with timed edges.

- Vertices and edges carry string ids; parallel edges and isolated
  vertices are preserved.
- Each edge is active on a left-closed, right-open interval [start, end).
- Dominators are computed from a designated entry over the subgraph of
  edges active at a chosen time.  Unreachable vertices are tracked
  separately and never enter the dominator tree.
- Single-edge changes (add/remove) and time changes trigger a
  recomputation and report the affected region: the vertices whose
  immediate dominator or reachability changed.
- Dominators are derived from a dataflow fixed point, never from DFS
  parents.
"""
from __future__ import annotations

import math
from collections import deque
from dataclasses import dataclass


@dataclass(frozen=True)
class Edge:
    id: str
    source: str
    target: str
    start: float = -math.inf  # inclusive
    end: float = math.inf     # exclusive

    def active_at(self, time: float) -> bool:
        return self.start <= time < self.end


class Graph:
    """Directed multigraph: parallel edges and isolated vertices allowed."""

    def __init__(self) -> None:
        self.vertices: set[str] = set()
        self.edges: dict[str, Edge] = {}

    def add_vertex(self, vertex_id: str) -> None:
        self.vertices.add(vertex_id)

    def add_edge(
        self,
        edge_id: str,
        source: str,
        target: str,
        start: float = -math.inf,
        end: float = math.inf,
    ) -> Edge:
        if edge_id in self.edges:
            raise ValueError(f"duplicate edge id: {edge_id!r}")
        self.vertices.add(source)
        self.vertices.add(target)
        edge = Edge(edge_id, source, target, start, end)
        self.edges[edge_id] = edge
        return edge

    def remove_edge(self, edge_id: str) -> Edge:
        return self.edges.pop(edge_id)

    def adjacency(self, time: float) -> tuple[dict[str, list[str]], dict[str, list[str]]]:
        succ = {v: [] for v in self.vertices}
        pred = {v: [] for v in self.vertices}
        for edge in self.edges.values():
            if edge.active_at(time):
                succ[edge.source].append(edge.target)
                pred[edge.target].append(edge.source)
        return succ, pred


_MISSING = object()


class DominatorTree:
    """Immediate dominators of every vertex reachable from the entry."""

    def __init__(self, graph: Graph, entry: str, time: float = 0.0) -> None:
        if entry not in graph.vertices:
            raise ValueError(f"unknown entry vertex: {entry!r}")
        self.graph = graph
        self.entry = entry
        self.time = time
        self._dom: dict[str, set[str]] = {}
        self._idom: dict[str, str | None] = {}
        self._unreachable: set[str] = set()
        self._recompute()

    # ------------------------------------------------------------------
    # queries
    # ------------------------------------------------------------------
    @property
    def unreachable(self) -> frozenset[str]:
        return frozenset(self._unreachable)

    def is_reachable(self, vertex: str) -> bool:
        return vertex in self._dom

    def idom(self, vertex: str) -> str | None:
        """Immediate dominator, None for the entry; unreachable is an error."""
        if vertex not in self._idom:
            raise ValueError(f"vertex {vertex!r} is unreachable from {self.entry!r}")
        return self._idom[vertex]

    def dominators(self, vertex: str) -> set[str]:
        if vertex not in self._dom:
            raise ValueError(f"vertex {vertex!r} is unreachable from {self.entry!r}")
        return set(self._dom[vertex])

    def dominates(self, a: str, b: str) -> bool:
        """True iff every path from the entry to b passes through a."""
        return b in self._dom and a in self._dom[b]

    def children(self, vertex: str) -> list[str]:
        return sorted(v for v, p in self._idom.items() if p == vertex)

    def counterexample(self, avoid: str, target: str) -> list[str] | None:
        """A path from the entry to target that avoids the given vertex.

        Returns None when no such path exists, i.e. the avoided vertex
        dominates the target (or it is the entry/target itself).
        """
        if target not in self._dom:
            raise ValueError(f"vertex {target!r} is unreachable from {self.entry!r}")
        if avoid in (self.entry, target):
            return None
        succ, _ = self.graph.adjacency(self.time)
        prev: dict[str, str | None] = {self.entry: None}
        queue: deque[str] = deque([self.entry])
        while queue:
            u = queue.popleft()
            if u == target:
                break
            for w in succ.get(u, ()):
                if w != avoid and w not in prev:
                    prev[w] = u
                    queue.append(w)
        if target not in prev:
            return None
        path = []
        node: str | None = target
        while node is not None:
            path.append(node)
            node = prev[node]
        path.reverse()
        return path

    # ------------------------------------------------------------------
    # updates: each returns the affected region
    # ------------------------------------------------------------------
    def add_edge(
        self,
        edge_id: str,
        source: str,
        target: str,
        start: float = -math.inf,
        end: float = math.inf,
    ) -> set[str]:
        self.graph.add_edge(edge_id, source, target, start, end)
        return self._refresh()

    def remove_edge(self, edge_id: str) -> set[str]:
        self.graph.remove_edge(edge_id)
        return self._refresh()

    def set_time(self, time: float) -> set[str]:
        self.time = time
        return self._refresh()

    # ------------------------------------------------------------------
    # internals
    # ------------------------------------------------------------------
    def _refresh(self) -> set[str]:
        old_idom = self._idom
        self._recompute()
        affected = set()
        for v in self.graph.vertices:
            if self._idom.get(v, _MISSING) != old_idom.get(v, _MISSING):
                affected.add(v)
        return affected

    def _recompute(self) -> None:
        succ, pred = self.graph.adjacency(self.time)

        reachable = {self.entry}
        queue: deque[str] = deque([self.entry])
        while queue:
            u = queue.popleft()
            for w in succ.get(u, ()):
                if w not in reachable:
                    reachable.add(w)
                    queue.append(w)
        self._unreachable = self.graph.vertices - reachable

        # Dataflow fixed point for dominator sets (not DFS parents).
        dom = {v: set(reachable) for v in reachable}
        dom[self.entry] = {self.entry}
        changed = True
        while changed:
            changed = False
            for v in sorted(reachable):
                if v == self.entry:
                    continue
                preds = [p for p in pred[v] if p in reachable]
                new = set(dom[preds[0]])
                for p in preds[1:]:
                    new &= dom[p]
                new.add(v)
                if new != dom[v]:
                    dom[v] = new
                    changed = True
        self._dom = dom

        # idom(v) is the strict dominator of v dominated by all other
        # strict dominators of v.
        self._idom = {self.entry: None}
        for v in sorted(reachable):
            if v == self.entry:
                continue
            strict = dom[v] - {v}
            for d in sorted(strict):
                if all(o in dom[d] for o in strict):
                    self._idom[v] = d
                    break
