"""Structural diff of two graph versions with an explicit rename map.

Produces a reversible Patch that distinguishes:
  * additions and removals of vertices/edges,
  * attribute and interval changes,
  * renames (from the explicit mapping) -- a renamed vertex keeps its edges;
    its incident edges are NOT reported as removed+added.
"""
from __future__ import annotations

from typing import Optional

from .model import DEFAULT_DIGEST_FN, DigestFn, Graph
from .ops import (
    AddEdge,
    AddVertex,
    AttrDelta,
    ChangeEdgeAttrs,
    ChangeEdgeEndpoints,
    ChangeEdgeInterval,
    ChangeVertexAttrs,
    ChangeVertexInterval,
    RemoveEdge,
    RemoveVertex,
    RenameVertex,
)
from .patch import Patch


class RenameConflictError(ValueError):
    """The explicit rename map is inconsistent with the two graphs."""

    def __init__(self, conflicts: list):
        self.conflicts = conflicts  # list of structured dicts
        summary = "; ".join(c["message"] for c in conflicts)
        super().__init__(f"rename map conflicts: {summary}")


def validate_renames(
    base: Graph, target: Graph, renames: dict
) -> None:
    """Check the explicit rename map; raise RenameConflictError on conflicts."""
    conflicts: list[dict] = []
    sources = set(renames)
    targets = set(renames.values())

    for old, new in sorted(renames.items()):
        if old == new:
            conflicts.append({
                "kind": "trivial_rename",
                "vertex": old,
                "message": f"rename {old!r} -> {new!r} is a no-op",
            })
        if old not in base.vertices:
            conflicts.append({
                "kind": "source_missing",
                "vertex": old,
                "message": f"rename source {old!r} is absent from the base graph",
            })
        if new not in target.vertices:
            conflicts.append({
                "kind": "target_missing",
                "vertex": new,
                "message": f"rename target {new!r} is absent from the target graph",
            })
        if new in base.vertices and new not in sources:
            conflicts.append({
                "kind": "target_collision",
                "vertex": new,
                "message": (
                    f"rename target {new!r} collides with a base vertex "
                    "that is not itself renamed"
                ),
            })
        if new in sources:
            conflicts.append({
                "kind": "chained_rename",
                "vertex": new,
                "message": (
                    f"rename target {new!r} is itself a rename source; "
                    "chained renames are not supported"
                ),
            })

    seen: dict[str, str] = {}
    for old, new in sorted(renames.items()):
        if new in seen and seen[new] != old:
            conflicts.append({
                "kind": "duplicate_target",
                "vertex": new,
                "message": (
                    f"rename sources {seen[new]!r} and {old!r} both map to {new!r}"
                ),
            })
        seen[new] = old

    if conflicts:
        raise RenameConflictError(conflicts)


def diff(
    base: Graph,
    target: Graph,
    renames: Optional[dict] = None,
    digest_fn: DigestFn = DEFAULT_DIGEST_FN,
) -> Patch:
    """Compute the patch transforming `base` into `target`.

    `renames` is an explicit {old_vertex_id: new_vertex_id} mapping; edges
    of a renamed vertex follow the rename instead of being reported as
    removed+added.
    """
    renames = dict(renames or {})
    validate_renames(base, target, renames)

    def mapped(vid: str) -> str:
        return renames.get(vid, vid)

    ops: list = []

    # 1. renames first so every later op speaks in post-rename names.
    for old in sorted(renames):
        ops.append(RenameVertex(old, renames[old]))

    base_vids = {mapped(v) for v in base.vertices}
    target_vids = set(target.vertices)

    # 2. edge removals (before-images use post-rename endpoint names,
    #    matching the graph state after the rename ops have run).
    base_eids = set(base.edges)
    target_eids = set(target.edges)
    for eid in sorted(base_eids - target_eids):
        e = base.edges[eid]
        ops.append(
            RemoveEdge(eid, mapped(e.src), mapped(e.dst), dict(e.attrs), e.interval)
        )

    # 3. vertex removals (never renamed-away vertices: those are in target).
    for vid in sorted(base_vids - target_vids):
        v = base.vertices[vid]  # vid == original id here (not renamed)
        ops.append(RemoveVertex(vid, dict(v.attrs), v.interval))

    # 4. vertex additions.
    for vid in sorted(target_vids - base_vids):
        v = target.vertices[vid]
        ops.append(AddVertex(vid, dict(v.attrs), v.interval))

    # 5. edge additions.
    for eid in sorted(target_eids - base_eids):
        e = target.edges[eid]
        ops.append(AddEdge(eid, e.src, e.dst, dict(e.attrs), e.interval))

    # 6. vertex content changes on the common (post-rename) ids.
    inv_renames = {new: old for old, new in renames.items()}
    for vid in sorted(base_vids & target_vids):
        old_id = inv_renames.get(vid, vid)
        before = base.vertices[old_id]
        after = target.vertices[vid]
        delta = AttrDelta.between(before.attrs, after.attrs)
        if not delta.is_empty():
            ops.append(ChangeVertexAttrs(vid, delta))
        if before.interval != after.interval:
            ops.append(ChangeVertexInterval(vid, before.interval, after.interval))

    # 7. edge content changes.  Endpoint comparison happens after mapping
    #    base endpoints through the rename map, so pure renames never show
    #    up as endpoint changes.
    for eid in sorted(base_eids & target_eids):
        before = base.edges[eid]
        after = target.edges[eid]
        eps_before = (mapped(before.src), mapped(before.dst))
        eps_after = (after.src, after.dst)
        if eps_before != eps_after:
            ops.append(ChangeEdgeEndpoints(eid, eps_before, eps_after))
        delta = AttrDelta.between(before.attrs, after.attrs)
        if not delta.is_empty():
            ops.append(ChangeEdgeAttrs(eid, delta))
        if before.interval != after.interval:
            ops.append(ChangeEdgeInterval(eid, before.interval, after.interval))

    return Patch(
        ops=ops,
        base_digest=digest_fn(base.canonical()),
        base_hash=base.content_hash(),
        target_digest=digest_fn(target.canonical()),
        target_hash=target.content_hash(),
        renames=renames,
    )
