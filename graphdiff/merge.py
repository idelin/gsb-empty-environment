"""Three-way replay: re-apply a patch (computed as base->target) onto a
different baseline `other` that carries its own independent modifications.

- Non-conflicting changes relocate: node references in the patch's ops are
  translated through the other branch's explicit rename map.
- Rename-vs-delete and same-name-different-identity (plus several more
  overlaps) produce structured Conflict objects; on any conflict nothing is
  applied.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Optional

from .diff import diff
from .model import Graph, attrs_equal
from .ops import (
    Op, RenameNode, AddNode, DelNode, AddEdge, DelEdge, ReconnectEdge,
    SetAttr, SetInterval,
)
from .patch import Patch


@dataclass
class Conflict:
    kind: str
    entity: str            # "node" | "edge"
    entity_id: str
    message: str
    ours: Any = None       # what the patch wants
    theirs: Any = None     # what the other baseline did

    def __repr__(self):
        return f"Conflict({self.kind}, {self.entity} {self.entity_id!r})"


@dataclass
class Relocation:
    op: str
    detail: str


@dataclass
class ReplayResult:
    ok: bool
    graph: Optional[Graph]
    conflicts: list[Conflict] = field(default_factory=list)
    relocations: list[Relocation] = field(default_factory=list)


class _BranchEffects:
    """Index of what a diff (base -> other) touched, for conflict lookup."""

    def __init__(self, delta: Patch):
        self.node_renamed: dict[str, str] = {}
        self.node_deleted: dict[str, DelNode] = {}
        self.node_added: dict[str, AddNode] = {}
        self.node_attrs: dict[str, dict[str, tuple]] = {}
        self.node_interval: dict[str, tuple] = {}
        self.edge_deleted: dict[str, DelEdge] = {}
        self.edge_added: dict[str, AddEdge] = {}
        self.edge_attrs: dict[str, dict[str, tuple]] = {}
        self.edge_interval: dict[str, tuple] = {}
        self.edge_reconnected: dict[str, tuple] = {}
        for op in delta.ops:
            if isinstance(op, RenameNode):
                self.node_renamed[op.old] = op.new
            elif isinstance(op, DelNode):
                self.node_deleted[op.vid] = op
            elif isinstance(op, AddNode):
                self.node_added[op.vid] = op
            elif isinstance(op, DelEdge):
                self.edge_deleted[op.eid] = op
            elif isinstance(op, AddEdge):
                self.edge_added[op.eid] = op
            elif isinstance(op, ReconnectEdge):
                self.edge_reconnected[op.eid] = (op.old_src, op.old_dst,
                                                 op.new_src, op.new_dst)
            elif isinstance(op, SetAttr):
                table = self.node_attrs if op.entity == "node" else self.edge_attrs
                table.setdefault(op.eid, {})[op.key] = (op.old, op.new)
            elif isinstance(op, SetInterval):
                table = self.node_interval if op.entity == "node" else self.edge_interval
                table[op.eid] = (op.old, op.new)


def replay(patch: Patch, other: Graph,
           other_renames: Optional[dict[str, str]] = None) -> ReplayResult:
    """Replay `patch` onto baseline `other`.

    `other_renames` is the explicit rename map describing how `other` differs
    from the patch's base (renames are never guessed)."""
    base = Graph.from_canonical(patch.base_content)
    delta = diff(base, other, other_renames or {})
    fx = _BranchEffects(delta)

    conflicts: list[Conflict] = []
    relocations: list[Relocation] = []
    out_ops: list[Op] = []
    # entities available as the replayed ops execute: starts from the other
    # baseline and is updated by every emitted op, so names freed or
    # supplied by the patch itself (rename-away, AddNode, ...) are tracked
    available = set(other.vertices)
    available_edges = set(other.edges)

    def relocate_node(vid: str) -> str:
        return fx.node_renamed.get(vid, vid)

    for op in patch.ops:
        translated = _translate(op, fx, other, relocate_node,
                                conflicts, relocations, available,
                                available_edges)
        if translated is None:
            continue  # conflict already recorded
        out_ops.extend(translated)
        for emitted in translated:
            if isinstance(emitted, AddNode):
                available.add(emitted.vid)
            elif isinstance(emitted, DelNode):
                available.discard(emitted.vid)
            elif isinstance(emitted, RenameNode):
                available.discard(emitted.old)
                available.add(emitted.new)
            elif isinstance(emitted, AddEdge):
                available_edges.add(emitted.eid)
            elif isinstance(emitted, DelEdge):
                available_edges.discard(emitted.eid)

    if conflicts:
        return ReplayResult(False, None, conflicts, relocations)

    new = other.copy()
    for op in out_ops:
        op.apply(new)
    return ReplayResult(True, new, [], relocations)


def _translate(op: Op, fx: _BranchEffects, other: Graph, relocate_node,
               conflicts: list[Conflict],
               relocations: list[Relocation], available: set,
               available_edges: set) -> Optional[list[Op]]:
    """Returns ops to emit, [] to skip, or None if a conflict was recorded."""

    if isinstance(op, RenameNode):
        if op.old in fx.node_deleted:
            conflicts.append(Conflict(
                "rename_vs_delete", "node", op.old,
                f"patch renames {op.old!r} to {op.new!r} but the other "
                f"baseline deleted it",
                ours=("rename", op.old, op.new), theirs=("delete", op.old)))
            return None
        if op.old in fx.node_renamed:
            if fx.node_renamed[op.old] == op.new:
                return []  # both branches did the same rename
            conflicts.append(Conflict(
                "rename_vs_rename", "node", op.old,
                f"patch renames {op.old!r} to {op.new!r}, other baseline "
                f"renamed it to {fx.node_renamed[op.old]!r}",
                ours=op.new, theirs=fx.node_renamed[op.old]))
            return None
        if op.new in available and op.new != op.old:
            conflicts.append(Conflict(
                "rename_target_occupied", "node", op.new,
                f"rename target {op.new!r} already exists in other baseline",
                ours=("rename", op.old, op.new), theirs=("exists", op.new)))
            return None
        return [op]

    if isinstance(op, AddNode):
        existing = fx.node_added.get(op.vid)
        if existing is not None or op.vid in available:
            same = existing is not None and attrs_equal(existing.attrs, op.attrs) \
                and existing.interval == op.interval
            if same:
                return []  # identical independent addition
            conflicts.append(Conflict(
                "same_name_different_identity", "node", op.vid,
                f"both branches added node {op.vid!r} with different content",
                ours={"attrs": op.attrs, "interval": op.interval.to_data()},
                theirs=("added" if existing else "present",)))
            return None
        return [op]

    if isinstance(op, DelNode):
        if op.vid in fx.node_deleted:
            return []  # already deleted by the other branch
        if op.vid in fx.node_renamed:
            conflicts.append(Conflict(
                "delete_vs_rename", "node", op.vid,
                f"patch deletes {op.vid!r} but the other baseline renamed it "
                f"to {fx.node_renamed[op.vid]!r}",
                ours=("delete",), theirs=("rename", fx.node_renamed[op.vid])))
            return None
        if op.vid in fx.node_attrs or op.vid in fx.node_interval:
            conflicts.append(Conflict(
                "delete_vs_modify", "node", op.vid,
                f"patch deletes {op.vid!r} but the other baseline modified it",
                ours=("delete",), theirs=("modify",)))
            return None
        return [op]

    if isinstance(op, SetAttr) or isinstance(op, SetInterval):
        table_attrs = fx.node_attrs if op.entity == "node" else fx.edge_attrs
        table_iv = fx.node_interval if op.entity == "node" else fx.edge_interval
        deleted = fx.node_deleted if op.entity == "node" else fx.edge_deleted
        renamed = fx.node_renamed if op.entity == "node" else {}
        if op.eid in deleted:
            conflicts.append(Conflict(
                "modify_vs_delete", op.entity, op.eid,
                f"patch modifies {op.entity} {op.eid!r} but the other "
                f"baseline deleted it",
                ours=("modify",), theirs=("delete",)))
            return None
        new_id = renamed.get(op.eid, op.eid)
        if isinstance(op, SetAttr):
            theirs = table_attrs.get(op.eid, {}).get(op.key)
            if theirs is not None:
                if theirs[1] == op.new:
                    return []  # same change on both branches
                conflicts.append(Conflict(
                    "attr_vs_attr", op.entity, op.eid,
                    f"both branches changed {op.entity} {op.eid!r}.{op.key!r}",
                    ours=op.new, theirs=theirs[1]))
                return None
            out = SetAttr(op.entity, new_id, op.key, op.old, op.new)
        else:
            if op.eid in table_iv:
                theirs = table_iv[op.eid]
                if theirs[1] == op.new:
                    return []
                conflicts.append(Conflict(
                    "interval_vs_interval", op.entity, op.eid,
                    f"both branches changed the interval of {op.entity} "
                    f"{op.eid!r}",
                    ours=op.new.to_data(), theirs=theirs[1].to_data()))
                return None
            out = SetInterval(op.entity, new_id, op.old, op.new)
        if new_id != op.eid:
            relocations.append(Relocation(
                type(op).__name__,
                f"{op.entity} {op.eid!r} relocated to {new_id!r}"))
        return [out]

    if isinstance(op, AddEdge):
        existing = fx.edge_added.get(op.eid)
        if existing is not None or op.eid in available_edges:
            same = existing is not None \
                and (existing.src, existing.dst, existing.interval) \
                    == (op.src, op.dst, op.interval) \
                and attrs_equal(existing.attrs, op.attrs)
            if same:
                return []
            conflicts.append(Conflict(
                "same_name_different_identity", "edge", op.eid,
                f"both branches added edge {op.eid!r} with different content",
                ours={"src": op.src, "dst": op.dst, "attrs": op.attrs},
                theirs=("added" if existing else "present",)))
            return None
        src, dst = relocate_node(op.src), relocate_node(op.dst)
        for ep in (src, dst):
            if ep not in available:
                conflicts.append(Conflict(
                    "endpoint_missing", "edge", op.eid,
                    f"endpoint {ep!r} of new edge {op.eid!r} is absent in the "
                    f"other baseline",
                    ours=(op.src, op.dst), theirs=("deleted", ep)))
                return None
        if (src, dst) != (op.src, op.dst):
            relocations.append(Relocation(
                "AddEdge", f"edge {op.eid!r} endpoints relocated "
                f"{op.src!r},{op.dst!r} -> {src!r},{dst!r}"))
        return [AddEdge(op.eid, src, dst, op.attrs, op.interval)]

    if isinstance(op, DelEdge):
        if op.eid in fx.edge_deleted:
            return []
        if op.eid in fx.edge_reconnected or op.eid in fx.edge_attrs \
                or op.eid in fx.edge_interval:
            conflicts.append(Conflict(
                "delete_vs_modify", "edge", op.eid,
                f"patch deletes edge {op.eid!r} but the other baseline "
                f"modified it",
                ours=("delete",), theirs=("modify",)))
            return None
        src, dst = relocate_node(op.src), relocate_node(op.dst)
        if (src, dst) != (op.src, op.dst):
            relocations.append(Relocation(
                "DelEdge", f"edge {op.eid!r} stored endpoints relocated "
                f"{op.src!r},{op.dst!r} -> {src!r},{dst!r}"))
        return [DelEdge(op.eid, src, dst, op.attrs, op.interval)]

    if isinstance(op, ReconnectEdge):
        if op.eid in fx.edge_deleted:
            conflicts.append(Conflict(
                "reconnect_vs_delete", "edge", op.eid,
                f"patch reconnects edge {op.eid!r} but the other baseline "
                f"deleted it",
                ours=("reconnect", op.new_src, op.new_dst),
                theirs=("delete",)))
            return None
        if op.eid in fx.edge_reconnected:
            _, _, tsrc, tdst = fx.edge_reconnected[op.eid]
            if (tsrc, tdst) == (op.new_src, op.new_dst):
                return []
            conflicts.append(Conflict(
                "reconnect_vs_reconnect", "edge", op.eid,
                f"both branches reconnected edge {op.eid!r} differently",
                ours=(op.new_src, op.new_dst), theirs=(tsrc, tdst)))
            return None
        src, dst = relocate_node(op.new_src), relocate_node(op.new_dst)
        old_src = relocate_node(op.old_src)
        old_dst = relocate_node(op.old_dst)
        if (src, dst) != (op.new_src, op.new_dst) \
                or (old_src, old_dst) != (op.old_src, op.old_dst):
            relocations.append(Relocation(
                "ReconnectEdge", f"edge {op.eid!r} relocated through renames"))
        return [ReconnectEdge(op.eid, old_src, old_dst, src, dst)]

    return [op]  # pragma: no cover - unknown ops pass through
