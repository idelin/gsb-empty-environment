"""Structural diff of two graph versions with an explicit rename map.

Distinguishes:
- additions / deletions of vertices and edges (by string ID);
- attribute and interval changes on surviving entities;
- endpoint changes of surviving edges (ReconnectEdge), in particular the
  endpoint moves caused purely by node renames -- edges keep their IDs, so a
  rename never shows up as delete+add of all incident edges.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional

from .model import Graph, values_equal
from .ops import (
    Op, MISSING, RenameNode, AddNode, DelNode, AddEdge, DelEdge,
    ReconnectEdge, SetAttr, SetInterval,
)
from .patch import Patch


@dataclass
class RenameConflict:
    kind: str          # missing_source | duplicate_target | target_occupied
    message: str
    nodes: list[str] = field(default_factory=list)


class RenameMapError(ValueError):
    def __init__(self, conflicts: list[RenameConflict]):
        self.conflicts = conflicts
        super().__init__("; ".join(c.message for c in conflicts))


def check_rename_map(base: Graph, rename_map: dict[str, str]) -> None:
    """Validate an explicit old->new rename map against the base graph.

    Raises RenameMapError with structured conflicts on:
    - source not present in base;
    - two sources mapping to the same target;
    - target already occupied by a vertex that is not itself renamed away.
    Simultaneous renames (incl. swaps A->B, B->A) are allowed.
    """
    conflicts: list[RenameConflict] = []
    sources = set(rename_map)

    for old in sorted(sources):
        if old not in base.vertices:
            conflicts.append(RenameConflict(
                "missing_source",
                f"rename source {old!r} does not exist in base graph",
                [old]))

    by_target: dict[str, list[str]] = {}
    for old, new in rename_map.items():
        by_target.setdefault(new, []).append(old)
    for new, olds in sorted(by_target.items()):
        if len(olds) > 1:
            conflicts.append(RenameConflict(
                "duplicate_target",
                f"rename target {new!r} is claimed by {sorted(olds)}",
                sorted(olds)))
        elif new in base.vertices and new not in sources:
            conflicts.append(RenameConflict(
                "target_occupied",
                f"rename target {new!r} already exists and is not renamed away",
                [olds[0], new]))

    if conflicts:
        raise RenameMapError(conflicts)


def _attr_delta(entity: str, eid: str, old_attrs: dict, new_attrs: dict,
                ops: list[Op]) -> None:
    for key in sorted(old_attrs.keys() | new_attrs.keys()):
        o, n = old_attrs.get(key, MISSING), new_attrs.get(key, MISSING)
        if not values_equal(o, n):
            ops.append(SetAttr(entity, eid, key, o, n))


def diff(base: Graph, target: Graph,
         rename_map: Optional[dict[str, str]] = None) -> Patch:
    rename_map = {o: n for o, n in (rename_map or {}).items() if o != n}
    check_rename_map(base, rename_map)

    ops: list[Op] = []
    for old, new in _sequence_renames(rename_map, set(base.vertices)):
        ops.append(RenameNode(old, new))

    def renamed(vid: str) -> str:
        return rename_map.get(vid, vid)

    # base viewed after renames; edge IDs are stable across renames
    base_v = {renamed(i): v for i, v in base.vertices.items()}
    base_e = {i: (renamed(e.src), renamed(e.dst), e.attrs, e.interval)
              for i, e in base.edges.items()}

    # Op order matters for sequential application:
    #   renames -> edge deletions -> node additions -> edge reconnects ->
    #   node deletions -> edge additions -> attribute/interval changes.
    # Reconnects must precede node deletions (a deleted node's surviving
    # edges are reconnected away first) and follow node additions (a
    # reconnect's new endpoint may be a newly added node).

    # -- edge deletions --------------------------------------------------------
    for eid in sorted(base_e):
        if eid not in target.edges:
            src, dst, attrs, iv = base_e[eid]
            ops.append(DelEdge(eid, src, dst, attrs, iv))

    # -- node additions ----------------------------------------------------------
    for vid in sorted(target.vertices):
        if vid not in base_v:
            v = target.vertices[vid]
            ops.append(AddNode(vid, v.attrs, v.interval))

    # -- edge reconnects on survivors --------------------------------------------
    for eid in sorted(base_e.keys() & target.edges.keys()):
        bsrc, bdst = base_e[eid][0], base_e[eid][1]
        te = target.edges[eid]
        if (bsrc, bdst) != (te.src, te.dst):
            ops.append(ReconnectEdge(eid, bsrc, bdst, te.src, te.dst))

    # -- node deletions ----------------------------------------------------------
    for vid in sorted(base_v):
        if vid not in target.vertices:
            v = base_v[vid]
            ops.append(DelNode(vid, v.attrs, v.interval))

    # -- edge additions ----------------------------------------------------------
    for eid in sorted(target.edges):
        if eid not in base_e:
            e = target.edges[eid]
            ops.append(AddEdge(eid, e.src, e.dst, e.attrs, e.interval))

    # -- attribute / interval changes on survivors -------------------------------
    for vid in sorted(base_v.keys() & target.vertices.keys()):
        bv, tv = base_v[vid], target.vertices[vid]
        _attr_delta("node", vid, bv.attrs, tv.attrs, ops)
        if bv.interval != tv.interval:
            ops.append(SetInterval("node", vid, bv.interval, tv.interval))
    for eid in sorted(base_e.keys() & target.edges.keys()):
        _, _, battrs, biv = base_e[eid]
        te = target.edges[eid]
        _attr_delta("edge", eid, battrs, te.attrs, ops)
        if biv != te.interval:
            ops.append(SetInterval("edge", eid, biv, te.interval))

    return Patch.between(base, target, ops)


def _sequence_renames(rename_map: dict[str, str],
                      taken: set[str]) -> list[tuple[str, str]]:
    """Expand a simultaneous rename map into a collision-free sequential
    order. Chains are emitted target-first; cycles are rotated through a
    temporary name."""
    remaining = dict(rename_map)
    seq: list[tuple[str, str]] = []
    temps: list[tuple[str, str]] = []
    counter = 0
    while remaining:
        progress = False
        for old, new in list(remaining.items()):
            if new not in remaining:
                seq.append((old, new))
                del remaining[old]
                progress = True
        if not progress:  # pure cycle: rotate through a temporary name
            old = next(iter(remaining))
            while True:
                tmp = f"__rename_tmp_{counter}__"
                counter += 1
                if tmp not in taken and tmp not in remaining \
                        and tmp not in rename_map.values():
                    break
            seq.append((old, tmp))
            temps.append((tmp, remaining[old]))
            del remaining[old]
    seq.extend(temps)
    return seq
