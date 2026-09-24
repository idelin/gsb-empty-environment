"""Hierarchical graph folding with incremental hyperedge projection.

Vertices, edges and groups all carry string ids.  Vertices are organised
into a forest of nested groups.  A *view* folds chosen groups into
supernodes and aggregates cross-group edges into hyperedges while keeping
provenance (the multiset of original edge ids) behind every hyperedge.

Invariants maintained incrementally:

* intra-supernode edges never surface as fake cross-group self-loops;
* ``Graph.member_count`` tracks nested vertex counts per group;
* ``View.edge_loc`` is the exact reverse index of ``View.hedges``;
* a single node move only touches its ancestor chain and its incident
  edges (see ``View.reindexed`` instrumentation).

Edges are active on a left-closed, right-open interval ``[start, end)``.
Batch application is atomic: any id conflict or hierarchy cycle rejects
the whole batch before any mutation happens.
"""

from __future__ import annotations

from dataclasses import dataclass

NEG_INF = float("-inf")
POS_INF = float("inf")


class GraphError(ValueError):
    """Base class for rejected graph operations."""


class IdConflictError(GraphError):
    """An id is reused inside the vertex/group or edge namespace."""


class UnknownIdError(GraphError):
    """An operation references an id that does not exist."""


class HierarchyCycleError(GraphError):
    """A parent assignment would close a cycle in the group hierarchy."""


@dataclass(frozen=True)
class Edge:
    id: str
    src: str
    dst: str
    start: float = NEG_INF
    end: float = POS_INF

    def active(self, t: float) -> bool:
        """Left-closed, right-open membership: start <= t < end."""
        return self.start <= t < self.end


@dataclass(frozen=True)
class AddVertex:
    id: str


@dataclass(frozen=True)
class AddGroup:
    id: str
    parent: str | None = None


@dataclass(frozen=True)
class AddEdge:
    id: str
    src: str
    dst: str
    start: float = NEG_INF
    end: float = POS_INF


@dataclass(frozen=True)
class Move:
    node: str
    parent: str | None


def _assert_acyclic(parent: dict[str, str | None], node: str) -> None:
    g = parent[node]
    while g is not None:
        if g == node:
            raise HierarchyCycleError(f"parent chain cycles at {node!r}")
        g = parent[g]


class Graph:
    """Base graph plus group hierarchy; mutations fan out to all views."""

    def __init__(self) -> None:
        self.vertices: set[str] = set()
        self.groups: set[str] = set()
        self.edges: dict[str, Edge] = {}
        self.parent: dict[str, str | None] = {}
        self.children: dict[str, set[str]] = {}
        self.incident: dict[str, set[str]] = {}
        self.member_count: dict[str, int] = {}
        self.views: list[View] = []
        self._undo: list[tuple[str, str | None]] = []
        self._version = 0

    # ------------------------------------------------------------------ API
    def apply(self, ops: list) -> None:
        """Atomically apply a batch; any error rejects the whole batch."""
        self._validate(ops)
        for op in ops:
            self._mutate(op)

    def move(self, node: str, parent: str | None) -> None:
        self.apply([Move(node, parent)])

    def undo(self) -> bool:
        """Revert the most recent recorded move (graph level)."""
        if not self._undo:
            return False
        node, old_parent = self._undo.pop()
        self._move(node, old_parent, record=False)
        return True

    # ---------------------------------------------------------- validation
    def _validate(self, ops: list) -> None:
        vertices = set(self.vertices)
        groups = set(self.groups)
        edge_ids = set(self.edges)
        parent = dict(self.parent)
        for op in ops:
            if isinstance(op, AddVertex):
                if op.id in vertices or op.id in groups:
                    raise IdConflictError(f"duplicate node id {op.id!r}")
                vertices.add(op.id)
                parent[op.id] = None
            elif isinstance(op, AddGroup):
                if op.id in vertices or op.id in groups:
                    raise IdConflictError(f"duplicate node id {op.id!r}")
                if op.parent is not None and op.parent not in groups:
                    raise UnknownIdError(f"unknown parent group {op.parent!r}")
                groups.add(op.id)
                parent[op.id] = op.parent
                _assert_acyclic(parent, op.id)
            elif isinstance(op, AddEdge):
                if op.id in edge_ids:
                    raise IdConflictError(f"duplicate edge id {op.id!r}")
                for endpoint in (op.src, op.dst):
                    if endpoint not in vertices:
                        raise UnknownIdError(f"unknown endpoint {endpoint!r}")
                edge_ids.add(op.id)
            elif isinstance(op, Move):
                if op.node not in vertices and op.node not in groups:
                    raise UnknownIdError(f"unknown node {op.node!r}")
                if op.parent is not None and op.parent not in groups:
                    raise UnknownIdError(f"unknown parent group {op.parent!r}")
                parent[op.node] = op.parent
                _assert_acyclic(parent, op.node)
            else:
                raise TypeError(f"unknown op {op!r}")

    # ----------------------------------------------------------- mutation
    def _mutate(self, op) -> None:
        self._version += 1
        if isinstance(op, AddVertex):
            self.vertices.add(op.id)
            self.parent[op.id] = None
            self.incident[op.id] = set()
        elif isinstance(op, AddGroup):
            self.groups.add(op.id)
            self.parent[op.id] = None
            self.children[op.id] = set()
            self.member_count[op.id] = 0
            if op.parent is not None:
                self.parent[op.id] = op.parent
                self.children[op.parent].add(op.id)
        elif isinstance(op, AddEdge):
            self.edges[op.id] = Edge(op.id, op.src, op.dst, op.start, op.end)
            self.incident[op.src].add(op.id)
            self.incident[op.dst].add(op.id)
            for view in self.views:
                view._reindex_edge(op.id)
        elif isinstance(op, Move):
            self._move(op.node, op.parent, record=True)

    def _move(self, node: str, new_parent: str | None, record: bool) -> None:
        old_parent = self.parent[node]
        if old_parent == new_parent:
            return
        if record:
            self._undo.append((node, old_parent))
        verts = self._subtree_vertices(node)
        n = len(verts)
        if old_parent is not None:
            self.children[old_parent].discard(node)
        g = old_parent
        while g is not None:
            self.member_count[g] -= n
            g = self.parent[g]
        self.parent[node] = new_parent
        if new_parent is not None:
            self.children[new_parent].add(node)
        g = new_parent
        while g is not None:
            self.member_count[g] += n
            g = self.parent[g]
        touched: set[str] = set()
        for v in verts:
            touched |= self.incident[v]
        for view in self.views:
            view._refresh_edges(touched)

    def _subtree_vertices(self, node: str) -> set[str]:
        if node in self.vertices:
            return {node}
        result: set[str] = set()
        stack = [node]
        while stack:
            for child in self.children.get(stack.pop(), ()):
                if child in self.vertices:
                    result.add(child)
                else:
                    stack.append(child)
        return result

    # ------------------------------------------------------------ views
    def _register_view(self, view: "View") -> None:
        self.views.append(view)

    def _unregister_view(self, view: "View") -> None:
        self.views.remove(view)


@dataclass
class Snapshot:
    time: float
    collapsed: frozenset[str]
    hedges: dict[tuple[str, str], list[str]]
    edge_loc: dict[str, tuple[str, str]]
    graph_version: int = 0


class View:
    """One independent folded projection of a graph at a point in time."""

    def __init__(self, graph: Graph, time: float = 0.0) -> None:
        self.graph = graph
        self.time = time
        self.collapsed: set[str] = set()
        self.hedges: dict[tuple[str, str], list[str]] = {}
        self.edge_loc: dict[str, tuple[str, str]] = {}
        self.history: list[Snapshot] = []
        self.reindexed = 0
        self._undo: list[tuple[str, str]] = []
        graph._register_view(self)
        self.reproject()

    def close(self) -> None:
        self.graph._unregister_view(self)

    # ------------------------------------------------------ projection
    def rep(self, vertex: str) -> str:
        """Visible supernode of a vertex: its outermost collapsed group."""
        result = vertex
        g = self.graph.parent[vertex]
        while g is not None:
            if g in self.collapsed:
                result = g
            g = self.graph.parent[g]
        return result

    def supernodes(self) -> set[str]:
        """Visible nodes: free vertices (incl. isolated) + visible groups."""
        nodes: set[str] = set()
        for gid in self.collapsed:
            anc = self.graph.parent[gid]
            hidden = False
            while anc is not None:
                if anc in self.collapsed:
                    hidden = True
                    break
                anc = self.graph.parent[anc]
            if not hidden:
                nodes.add(gid)
        for v in self.graph.vertices:
            if self.rep(v) == v:
                nodes.add(v)
        return nodes

    def size(self, supernode: str) -> int:
        """Nested member count of a supernode (1 for a bare vertex)."""
        return self.graph.member_count.get(supernode, 1)

    def _place(self, eid: str) -> tuple[str, str] | None:
        e = self.graph.edges[eid]
        if not e.active(self.time):
            return None
        a, b = self.rep(e.src), self.rep(e.dst)
        if a == b:
            return None
        return (a, b)

    def _reindex_edge(self, eid: str) -> None:
        self.reindexed += 1
        new = self._place(eid)
        old = self.edge_loc.get(eid)
        if old == new:
            return
        if old is not None:
            bucket = self.hedges[old]
            bucket.remove(eid)
            if not bucket:
                del self.hedges[old]
            del self.edge_loc[eid]
        if new is not None:
            self.hedges.setdefault(new, []).append(eid)
            self.edge_loc[eid] = new

    def _refresh_edges(self, edge_ids: set[str]) -> None:
        for eid in edge_ids:
            self._reindex_edge(eid)

    def _refresh_group(self, gid: str) -> None:
        touched: set[str] = set()
        for v in self.graph._subtree_vertices(gid):
            touched |= self.graph.incident[v]
        self._refresh_edges(touched)

    def reproject(self) -> None:
        """Full rebuild from the base graph (the reference semantics)."""
        self.hedges = {}
        self.edge_loc = {}
        for eid in self.graph.edges:
            self._reindex_edge(eid)

    # --------------------------------------------------------- operations
    def collapse(self, gid: str) -> None:
        if gid not in self.graph.groups:
            raise UnknownIdError(f"unknown group {gid!r}")
        if gid in self.collapsed:
            return
        self.collapsed.add(gid)
        self._undo.append(("expand", gid))
        self._refresh_group(gid)

    def expand(self, gid: str) -> None:
        if gid not in self.collapsed:
            return
        self.collapsed.discard(gid)
        self._undo.append(("collapse", gid))
        self._refresh_group(gid)

    def undo(self) -> bool:
        """Revert the most recent collapse/expand of this view."""
        if not self._undo:
            return False
        action, gid = self._undo.pop()
        if action == "expand":
            self.collapsed.discard(gid)
        else:
            self.collapsed.add(gid)
        self._refresh_group(gid)
        return True

    def set_time(self, t: float) -> None:
        self.time = t
        self.reproject()

    # ---------------------------------------------------------- snapshots
    def snapshot(self) -> Snapshot:
        snap = Snapshot(
            time=self.time,
            collapsed=frozenset(self.collapsed),
            hedges={k: list(v) for k, v in self.hedges.items()},
            edge_loc=dict(self.edge_loc),
            graph_version=self.graph._version,
        )
        self.history.append(snap)
        return snap

    def restore(self, snap: Snapshot) -> None:
        """Reset (time, collapsed) from a snapshot, then reproject.

        The stored index remains valid historical evidence; the live
        index is rebuilt against the *current* graph so a restore can
        never resurrect stale hyperedges after graph mutations.
        """
        self.time = snap.time
        self.collapsed = set(snap.collapsed)
        self.reproject()

    # ------------------------------------------------------------ queries
    def hyperedge_sources(self, src: str, dst: str) -> list[str]:
        """Provenance: original edge ids behind hyperedge (src, dst)."""
        return list(self.hedges.get((src, dst), []))

    def locate(self, eid: str) -> tuple[str, str] | None:
        """Reverse lookup: which hyperedge currently carries this edge."""
        if eid not in self.graph.edges:
            raise UnknownIdError(f"unknown edge {eid!r}")
        return self.edge_loc.get(eid)
