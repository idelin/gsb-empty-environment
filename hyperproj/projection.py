"""Incremental hierarchical graph projection.

A :class:`Projection` keeps a base graph (string-id vertices and directed
multi-edges with half-open time intervals) plus a forest of groups.  Any set
of groups may be *collapsed* into super-nodes; edges crossing the visible
boundary are aggregated into super-edges that remember their original edge
ids (evidence), while edges whose endpoints coincide on the same visible
node stay *internal* and are never reported as fake cross-group self-loops.

The derived index is maintained incrementally:

* moving a single vertex touches only its ancestor chain (nested member
  counts) and its incident edges (super-edge multiplicities and the
  original-edge reverse lookup);
* collapsing/expanding/moving a group touches only that subtree's vertices
  and their incident edges.

All mutations go through :meth:`Projection.apply`, which validates the whole
batch and rejects it atomically (hierarchy cycles, id conflicts, unknown
ids, invalid intervals) before anything is committed.  Every committed
batch pushes an undo snapshot; named snapshots and independent forks
provide multiple coexisting collapsed views of the same machinery.
"""

from __future__ import annotations

from collections import defaultdict
from copy import deepcopy

from .errors import (
    HierarchyCycleError,
    IdConflictError,
    InvalidIntervalError,
    ProjectionError,
    UnknownIdError,
)
from .model import (
    AddEdge,
    AddGroup,
    AddVertex,
    Collapse,
    Edge,
    Expand,
    MoveGroup,
    MoveVertex,
)


class Projection:
    def __init__(self) -> None:
        # --- primitive state (the only data snapshots/undo need) ---
        self.vertices: dict[str, str | None] = {}  # vertex -> parent group
        self.groups: dict[str, str | None] = {}    # group  -> parent group
        self.edges: dict[str, Edge] = {}
        self.collapsed: set[str] = set()           # antichain of group ids
        self._history: list[tuple] = []
        self._snapshots: dict[str, tuple] = {}
        self._rebuild()

    # ------------------------------------------------------------------
    # derived index (rebuilt on restore, patched incrementally otherwise)
    # ------------------------------------------------------------------

    def _rebuild(self) -> None:
        self._members: dict[str | None, set[str]] = defaultdict(set)
        self._children: dict[str | None, set[str]] = defaultdict(set)
        self._count: dict[str, int] = {g: 0 for g in self.groups}
        self._incident: dict[str, set[str]] = defaultdict(set)
        self._visible: dict[str, str] = {}
        self._edge_loc: dict[str, tuple[str, str]] = {}
        self._cross: dict[tuple[str, str], set[str]] = defaultdict(set)
        self._internal: dict[str, set[str]] = defaultdict(set)
        for v, g in self.vertices.items():
            self._members[g].add(v)
        for g, p in self.groups.items():
            self._children[p].add(g)
        for v in self.vertices:
            g = self.vertices[v]
            while g is not None:
                self._count[g] += 1
                g = self.groups[g]
        for eid, e in self.edges.items():
            self._incident[e.src].add(eid)
            self._incident[e.dst].add(eid)
        for v in self.vertices:
            self._visible[v] = self._compute_visible(v)
        for eid in self.edges:
            self._reindex_edge(eid)

    def _compute_visible(self, vid: str) -> str:
        """Visible node for a vertex: its collapsed ancestor nearest the
        root (unique because ``collapsed`` is kept an antichain), else the
        vertex itself."""
        node = vid
        g = self.vertices[vid]
        while g is not None:
            if g in self.collapsed:
                node = g
            g = self.groups[g]
        return node

    def _reindex_edge(self, eid: str) -> None:
        e = self.edges[eid]
        loc = (self._visible[e.src], self._visible[e.dst])
        old = self._edge_loc.get(eid)
        if old == loc:
            return
        if old is not None:
            self._remove_loc(old, eid)
        self._edge_loc[eid] = loc
        u, v = loc
        if u == v:
            self._internal[u].add(eid)
        else:
            self._cross[(u, v)].add(eid)

    def _remove_loc(self, loc: tuple[str, str], eid: str) -> None:
        u, v = loc
        if u == v:
            bucket, key = self._internal, u
        else:
            bucket, key = self._cross, (u, v)
        bucket[key].discard(eid)
        if not bucket[key]:
            del bucket[key]

    def _refresh_vertices(self, vids: set[str]) -> None:
        """Recompute visible nodes for ``vids`` and reindex exactly the
        edges incident to them."""
        touched: set[str] = set()
        for v in vids:
            self._visible[v] = self._compute_visible(v)
            touched |= self._incident[v]
        for eid in touched:
            self._reindex_edge(eid)

    def _subtree_vertices(self, gid: str) -> list[str]:
        out: list[str] = []
        stack = [gid]
        while stack:
            g = stack.pop()
            out.extend(self._members[g])
            stack.extend(self._children[g])
        return out

    def _descendant_groups(self, gid: str) -> list[str]:
        out: list[str] = []
        stack = list(self._children[gid])
        while stack:
            g = stack.pop()
            out.append(g)
            stack.extend(self._children[g])
        return out

    # ------------------------------------------------------------------
    # atomic batch application
    # ------------------------------------------------------------------

    def apply(self, *ops) -> None:
        """Validate and commit a batch of ops atomically.

        On any :class:`ProjectionError` the entire batch is rolled back and
        nothing is recorded in the undo history.
        """
        if len(ops) == 1 and isinstance(ops[0], (list, tuple)):
            ops = tuple(ops[0])
        backup = self._snapshot_state()
        try:
            for op in ops:
                self._apply_one(op)
        except ProjectionError:
            self._restore_state(backup)
            raise
        self._history.append(backup)

    def _apply_one(self, op) -> None:
        if isinstance(op, AddVertex):
            self._add_vertex(op.vid, op.group)
        elif isinstance(op, AddGroup):
            self._add_group(op.gid, op.parent)
        elif isinstance(op, AddEdge):
            self._add_edge(op.eid, op.src, op.dst, op.start, op.end)
        elif isinstance(op, MoveVertex):
            self._move_vertex(op.vid, op.group)
        elif isinstance(op, MoveGroup):
            self._move_group(op.gid, op.parent)
        elif isinstance(op, Collapse):
            self._collapse(op.gid)
        elif isinstance(op, Expand):
            self._expand(op.gid)
        else:
            raise ProjectionError(f"unknown operation: {op!r}")

    # ------------------------------------------------------------------
    # individual operations (validate first, then mutate incrementally)
    # ------------------------------------------------------------------

    def _check_node_id_free(self, nid: str) -> None:
        if nid in self.vertices or nid in self.groups:
            raise IdConflictError(f"node id already in use: {nid!r}")

    def _check_group(self, gid: str | None) -> None:
        if gid is not None and gid not in self.groups:
            raise UnknownIdError(f"unknown group: {gid!r}")

    def _add_vertex(self, vid: str, group: str | None) -> None:
        self._check_node_id_free(vid)
        self._check_group(group)
        self.vertices[vid] = group
        self._members[group].add(vid)
        g = group
        while g is not None:
            self._count[g] += 1
            g = self.groups[g]
        self._visible[vid] = self._compute_visible(vid)

    def _add_group(self, gid: str, parent: str | None) -> None:
        self._check_node_id_free(gid)
        self._check_group(parent)
        self.groups[gid] = parent
        self._children[parent].add(gid)
        self._count[gid] = 0

    def _add_edge(self, eid, src, dst, start, end) -> None:
        if eid in self.edges:
            raise IdConflictError(f"edge id already in use: {eid!r}")
        for v in (src, dst):
            if v not in self.vertices:
                raise UnknownIdError(f"unknown vertex: {v!r}")
        if start is not None and end is not None and not start < end:
            raise InvalidIntervalError(
                f"edge {eid!r}: require start < end, got [{start}, {end})"
            )
        self.edges[eid] = Edge(eid, src, dst, start, end)
        self._incident[src].add(eid)
        self._incident[dst].add(eid)
        self._reindex_edge(eid)

    def _move_vertex(self, vid: str, group: str | None) -> None:
        if vid not in self.vertices:
            raise UnknownIdError(f"unknown vertex: {vid!r}")
        self._check_group(group)
        old = self.vertices[vid]
        if old == group:
            return
        # nested member counts: walk only the two ancestor chains
        g = old
        while g is not None:
            self._count[g] -= 1
            g = self.groups[g]
        g = group
        while g is not None:
            self._count[g] += 1
            g = self.groups[g]
        self._members[old].discard(vid)
        self._members[group].add(vid)
        self.vertices[vid] = group
        # super-edge multiplicities + reverse lookup: only incident edges
        self._refresh_vertices({vid})

    def _move_group(self, gid: str, parent: str | None) -> None:
        if gid not in self.groups:
            raise UnknownIdError(f"unknown group: {gid!r}")
        self._check_group(parent)
        # cycle check: the new parent must not lie inside gid's subtree
        g = parent
        while g is not None:
            if g == gid:
                raise HierarchyCycleError(
                    f"moving {gid!r} under {parent!r} would create a cycle"
                )
            g = self.groups[g]
        old = self.groups[gid]
        if old == parent:
            return
        cnt = self._count[gid]
        g = old
        while g is not None:
            self._count[g] -= cnt
            g = self.groups[g]
        g = parent
        while g is not None:
            self._count[g] += cnt
            g = self.groups[g]
        self._children[old].discard(gid)
        self._children[parent].add(gid)
        self.groups[gid] = parent
        self._refresh_vertices(set(self._subtree_vertices(gid)))

    def _collapse(self, gid: str) -> None:
        if gid not in self.groups:
            raise UnknownIdError(f"unknown group: {gid!r}")
        # keep the collapsed set an antichain: hidden descendants drop out
        for d in self._descendant_groups(gid):
            self.collapsed.discard(d)
        self.collapsed.add(gid)
        self._refresh_vertices(set(self._subtree_vertices(gid)))

    def _expand(self, gid: str) -> None:
        if gid not in self.groups:
            raise UnknownIdError(f"unknown group: {gid!r}")
        if gid not in self.collapsed:
            return
        self.collapsed.discard(gid)
        self._refresh_vertices(set(self._subtree_vertices(gid)))

    # ------------------------------------------------------------------
    # history: undo, named snapshots, independent views
    # ------------------------------------------------------------------

    def _snapshot_state(self) -> tuple:
        return (
            deepcopy(self.vertices),
            deepcopy(self.groups),
            deepcopy(self.edges),
            set(self.collapsed),
        )

    def _restore_state(self, state: tuple) -> None:
        self.vertices, self.groups, self.edges, self.collapsed = deepcopy(state)
        self._rebuild()

    def undo(self) -> bool:
        """Revert the most recent committed batch.  Returns False if empty."""
        if not self._history:
            return False
        self._restore_state(self._history.pop())
        return True

    def save_snapshot(self, tag: str) -> None:
        self._snapshots[tag] = self._snapshot_state()

    def restore(self, tag: str) -> None:
        """Jump to a named snapshot; the jump itself is undoable."""
        if tag not in self._snapshots:
            raise UnknownIdError(f"unknown snapshot: {tag!r}")
        self._history.append(self._snapshot_state())
        self._restore_state(self._snapshots[tag])

    def snapshot_tags(self) -> list[str]:
        return sorted(self._snapshots)

    def fork(self) -> "Projection":
        """An independent collapsed view: same state, no shared mutation."""
        twin = Projection()
        twin._restore_state(self._snapshot_state())
        return twin

    # ------------------------------------------------------------------
    # queries
    # ------------------------------------------------------------------

    def _active(self, eid: str, at: float | None) -> bool:
        return at is None or self.edges[eid].active_at(at)

    def supernodes(self) -> set[str]:
        """Visible nodes: every vertex's image plus (possibly empty)
        collapsed groups.  Isolated vertices are preserved."""
        return set(self._visible.values()) | set(self.collapsed)

    def superedges(self, at: float | None = None) -> dict[tuple[str, str], list[str]]:
        """Map ``(U, V) -> sorted original edge ids`` for cross edges.

        Internal edges (U == V) are excluded by construction, so a collapsed
        group never shows a fake self-loop here.
        """
        out: dict[tuple[str, str], list[str]] = {}
        for key, ids in self._cross.items():
            active = sorted(e for e in ids if self._active(e, at))
            if active:
                out[key] = active
        return out

    def evidence(self, u: str, v: str, at: float | None = None) -> list[str]:
        """Original edge ids backing the super-edge ``u -> v``."""
        return sorted(e for e in self._cross.get((u, v), ()) if self._active(e, at))

    def multiplicity(self, u: str, v: str, at: float | None = None) -> int:
        return len(self.evidence(u, v, at))

    def internal_edges(self, node: str, at: float | None = None) -> list[str]:
        """Edges whose both endpoints are currently inside ``node``."""
        return sorted(e for e in self._internal.get(node, ()) if self._active(e, at))

    def internal_map(self, at: float | None = None) -> dict[str, list[str]]:
        out: dict[str, list[str]] = {}
        for node, ids in self._internal.items():
            active = sorted(e for e in ids if self._active(e, at))
            if active:
                out[node] = active
        return out

    def edge_location(self, eid: str) -> tuple[str, str]:
        """Reverse lookup: current visible endpoints of an original edge."""
        if eid not in self._edge_loc:
            raise UnknownIdError(f"unknown edge: {eid!r}")
        return self._edge_loc[eid]

    def member_count(self, gid: str) -> int:
        """Total number of descendant vertices (nested member count)."""
        if gid not in self.groups:
            raise UnknownIdError(f"unknown group: {gid!r}")
        return self._count[gid]

    def visible_node(self, vid: str) -> str:
        if vid not in self._visible:
            raise UnknownIdError(f"unknown vertex: {vid!r}")
        return self._visible[vid]

    def visible_map(self) -> dict[str, str]:
        return dict(self._visible)
