"""Dynamic multigraph maintaining bridges, articulation points and
biconnected components under batch edge insertions and deletions.

Semantics
---------
* Vertices and edges are identified by strings.  Isolated vertices are
  kept in the graph until explicitly removed; parallel edges and
  self-loops follow multigraph semantics (each edge is a distinct
  entity keyed by its edge id).
* Every edge carries a validity interval ``[start, end)`` -- left
  closed, right open.  An edge is *active* at time ``t`` iff
  ``start <= t < end``.  Passing ``t=None`` to a query ignores
  intervals and considers every edge.
* Batch updates (``add_edges`` / ``remove_edges``) mutate the stored
  graph; analysis results are recomputed lazily and cached per
  (graph version, t), so the result of any batch update is identical
  to running the full algorithm on the current graph.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Iterable, Iterator, NamedTuple, Optional, Sequence


@dataclass(frozen=True)
class Edge:
    """An undirected multigraph edge with a half-open validity interval."""

    id: str
    u: str
    v: str
    start: float = -math.inf
    end: float = math.inf

    def is_active(self, t: float) -> bool:
        """Left-closed, right-open membership: ``start <= t < end``."""
        return self.start <= t < self.end

    @property
    def is_loop(self) -> bool:
        return self.u == self.v


class Analysis(NamedTuple):
    """Snapshot of the graph decomposition at a given time."""

    bridges: frozenset  # frozenset[str] of edge ids
    articulation_points: frozenset  # frozenset[str] of vertex ids
    blocks: tuple  # tuple[frozenset[str]] edge-id sets (biconnected components)
    components: tuple  # tuple[frozenset[str]] vertex sets (connected components)


class DynamicMultigraph:
    """Multigraph with batch edge updates and cached decomposition queries."""

    def __init__(self) -> None:
        self._vertices: set[str] = set()
        self._edges: dict[str, Edge] = {}
        self._version = 0
        self._cache: dict[tuple[int, Optional[float]], Analysis] = {}

    # ------------------------------------------------------------------
    # mutation
    # ------------------------------------------------------------------
    def add_vertex(self, vertex: str) -> None:
        """Add a vertex (kept even when it has no incident edges)."""
        self._vertices.add(vertex)
        self._version += 1
        self._cache.clear()

    def remove_vertex(self, vertex: str) -> None:
        """Remove a vertex together with every edge incident to it."""
        if vertex not in self._vertices:
            raise KeyError(f"unknown vertex: {vertex!r}")
        self._vertices.discard(vertex)
        self._edges = {
            eid: e
            for eid, e in self._edges.items()
            if e.u != vertex and e.v != vertex
        }
        self._version += 1
        self._cache.clear()

    def add_edges(self, specs: Iterable[Sequence]) -> None:
        """Batch-add edges.

        Each spec is ``(edge_id, u, v)`` or ``(edge_id, u, v, start, end)``.
        Endpoint vertices are created implicitly.  Re-adding an existing
        edge id raises ``ValueError`` so parallel edges stay distinct.
        """
        new_edges: list[Edge] = []
        for spec in specs:
            if len(spec) not in (3, 5):
                raise ValueError(
                    "edge spec must be (id, u, v) or (id, u, v, start, end)"
                )
            edge_id, u, v = spec[0], spec[1], spec[2]
            start = spec[3] if len(spec) == 5 else -math.inf
            end = spec[4] if len(spec) == 5 else math.inf
            if not start < end:
                raise ValueError(f"empty interval for edge {edge_id!r}")
            if edge_id in self._edges or any(e.id == edge_id for e in new_edges):
                raise ValueError(f"duplicate edge id: {edge_id!r}")
            new_edges.append(Edge(edge_id, u, v, start, end))
        for edge in new_edges:
            self._edges[edge.id] = edge
            self._vertices.add(edge.u)
            self._vertices.add(edge.v)
        if new_edges:
            self._version += 1
            self._cache.clear()

    def remove_edges(self, edge_ids: Iterable[str]) -> None:
        """Batch-remove edges by id.  Vertices (now possibly isolated) stay."""
        removed = False
        for edge_id in edge_ids:
            if edge_id not in self._edges:
                raise KeyError(f"unknown edge id: {edge_id!r}")
            del self._edges[edge_id]
            removed = True
        if removed:
            self._version += 1
            self._cache.clear()

    # ------------------------------------------------------------------
    # inspection
    # ------------------------------------------------------------------
    @property
    def vertices(self) -> set[str]:
        return set(self._vertices)

    @property
    def edges(self) -> dict[str, Edge]:
        return dict(self._edges)

    def active_edges(self, t: Optional[float] = None) -> list[Edge]:
        """Edges active at ``t`` (all edges when ``t`` is None)."""
        if t is None:
            return list(self._edges.values())
        return [e for e in self._edges.values() if e.is_active(t)]

    # ------------------------------------------------------------------
    # queries
    # ------------------------------------------------------------------
    def bridges(self, t: Optional[float] = None) -> frozenset:
        return self._analyze(t).bridges

    def articulation_points(self, t: Optional[float] = None) -> frozenset:
        return self._analyze(t).articulation_points

    def biconnected_components(self, t: Optional[float] = None) -> tuple:
        """Edge-id sets; self-loops and bridges are singleton blocks."""
        return self._analyze(t).blocks

    def connected_components(self, t: Optional[float] = None) -> tuple:
        return self._analyze(t).components

    def separated_by(
        self, edge_id: str, t: Optional[float] = None
    ) -> Optional[tuple[frozenset, frozenset]]:
        """Vertex sets split apart if ``edge_id`` fails at time ``t``.

        Returns ``(side_u, side_v)`` -- the vertices reachable from each
        endpoint after removing the edge -- when the edge is a bridge,
        and ``None`` otherwise (non-bridge, self-loop, parallel edge or
        edge inactive at ``t``).
        """
        if edge_id not in self._edges:
            raise KeyError(f"unknown edge id: {edge_id!r}")
        analysis = self._analyze(t)
        if edge_id not in analysis.bridges:
            return None
        edge = self._edges[edge_id]
        adj = self._adjacency(t, skip_edge=edge_id)
        return (self._reach(edge.u, adj), self._reach(edge.v, adj))

    # ------------------------------------------------------------------
    # internals
    # ------------------------------------------------------------------
    def _adjacency(
        self, t: Optional[float], skip_edge: Optional[str] = None
    ) -> dict[str, list[tuple[str, str]]]:
        adj: dict[str, list[tuple[str, str]]] = {v: [] for v in self._vertices}
        for edge in self.active_edges(t):
            if edge.id == skip_edge or edge.is_loop:
                continue
            adj[edge.u].append((edge.v, edge.id))
            adj[edge.v].append((edge.u, edge.id))
        return adj

    @staticmethod
    def _reach(source: str, adj: dict[str, list[tuple[str, str]]]) -> frozenset:
        seen = {source}
        stack = [source]
        while stack:
            u = stack.pop()
            for w, _ in adj[u]:
                if w not in seen:
                    seen.add(w)
                    stack.append(w)
        return frozenset(seen)

    def _analyze(self, t: Optional[float]) -> Analysis:
        key = (self._version, t)
        cached = self._cache.get(key)
        if cached is not None:
            return cached
        result = self._tarjan(t)
        self._cache[key] = result
        return result

    def _tarjan(self, t: Optional[float]) -> Analysis:
        """Iterative Tarjan over the active subgraph (multigraph-safe).

        Parallel edges are handled by skipping only the exact parent
        edge id (a second edge to the parent is a genuine back edge);
        self-loops are excluded from the DFS and reported as singleton
        blocks since they never affect connectivity.
        """
        adj = self._adjacency(t)
        loops = [e.id for e in self.active_edges(t) if e.is_loop]

        disc: dict[str, int] = {}
        low: dict[str, int] = {}
        parent: dict[str, Optional[str]] = {}
        parent_edge: dict[str, Optional[str]] = {}
        children: dict[str, int] = {}
        bridges: set[str] = set()
        aps: set[str] = set()
        blocks: list[frozenset] = []
        edge_stack: list[str] = []
        timer = 0

        for root in sorted(self._vertices):
            if root in disc:
                continue
            disc[root] = low[root] = timer
            timer += 1
            parent[root] = None
            parent_edge[root] = None
            children[root] = 0
            stack: list[tuple[str, Iterator]] = [(root, iter(adj[root]))]
            while stack:
                u, it = stack[-1]
                advanced = False
                for w, eid in it:
                    if eid == parent_edge[u]:
                        continue  # the single tree edge to the parent
                    if w not in disc:
                        edge_stack.append(eid)
                        parent[w] = u
                        parent_edge[w] = eid
                        disc[w] = low[w] = timer
                        timer += 1
                        children[w] = 0
                        children[u] += 1
                        stack.append((w, iter(adj[w])))
                        advanced = True
                        break
                    if disc[w] < disc[u]:
                        # back edge to an ancestor (incl. parallel edges)
                        edge_stack.append(eid)
                        low[u] = min(low[u], disc[w])
                if advanced:
                    continue
                stack.pop()
                p = parent[u]
                if p is None:
                    continue
                low[p] = min(low[p], low[u])
                pe = parent_edge[u]
                if low[u] >= disc[p]:
                    if parent[p] is not None or children[p] >= 2:
                        aps.add(p)
                    block: list[str] = []
                    while True:
                        top = edge_stack.pop()
                        block.append(top)
                        if top == pe:
                            break
                    blocks.append(frozenset(block))
                if low[u] > disc[p]:
                    bridges.add(pe)

        for loop_id in loops:
            blocks.append(frozenset({loop_id}))

        components = self._components(adj)
        return Analysis(
            bridges=frozenset(bridges),
            articulation_points=frozenset(aps),
            blocks=tuple(blocks),
            components=components,
        )

    def _components(self, adj: dict[str, list[tuple[str, str]]]) -> tuple:
        seen: set[str] = set()
        comps: list[frozenset] = []
        for v in sorted(self._vertices):
            if v in seen:
                continue
            comp = self._reach(v, adj)
            seen |= comp
            comps.append(comp)
        return tuple(comps)
