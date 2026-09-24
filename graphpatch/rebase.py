"""Three-way replay: relocate a patch onto a modified baseline.

Given a patch P: B -> T and an alternate baseline B' (B plus independent
modifications), replay produces a rebased patch applicable to B' plus a
list of structured conflicts for the parts that cannot be relocated.

The replay simulates the patch over a *working copy* of B', so preceding
relocated renames/additions are visible to later operations.

Non-conflicting modifications in B' are left alone (relocation); genuine
conflicts -- e.g. rename-vs-delete, or a rename target name already taken
by a *different* vertex (same name, different identity) -- are reported as
structured Conflict records.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional

from .model import DEFAULT_DIGEST_FN, DigestFn, Graph
from .ops import (
    MISSING,
    encode_value,
    AddEdge,
    AddVertex,
    AttrDelta,
    ChangeEdgeAttrs,
    ChangeEdgeEndpoints,
    ChangeEdgeInterval,
    ChangeVertexAttrs,
    ChangeVertexInterval,
    Op,
    RemoveEdge,
    RemoveVertex,
    RenameVertex,
    attrs_equal,
    value_equal,
)
from .patch import Patch, PreconditionError, _apply_op

# Conflict kinds
RENAME_VS_DELETE = "rename_vs_delete"
SAME_NAME_DIFFERENT_IDENTITY = "same_name_different_identity"
REMOVED_VS_MODIFIED = "removed_vs_modified"
MODIFIED_VS_REMOVED = "modified_vs_removed"
ATTR_VALUE = "attr_value"
INTERVAL_VALUE = "interval_value"
EDGE_IDENTITY_MISMATCH = "edge_identity_mismatch"
EDGE_ENDPOINTS = "edge_endpoints"
ENDPOINT_MISSING = "endpoint_missing"


@dataclass(frozen=True)
class Conflict:
    kind: str
    entity: str           # e.g. "vertex:A" or "edge:e1"
    op: str               # repr of the patch op that could not be relocated
    message: str
    detail: dict = field(default_factory=dict)


@dataclass
class RebaseResult:
    patch: Patch
    conflicts: list

    @property
    def ok(self) -> bool:
        return not self.conflicts


def replay(
    patch: Patch,
    alt_base: Graph,
    digest_fn: DigestFn = DEFAULT_DIGEST_FN,
) -> RebaseResult:
    """Replay `patch` (built against its own base B) onto `alt_base` B'."""
    conflicts: list[Conflict] = []
    new_ops: list[Op] = []
    work = alt_base.copy()

    def relocate(op: Op) -> None:
        new_ops.append(op)
        _apply_op(work, op)

    def conflict(kind: str, entity: str, op: Op, message: str, detail=None):
        conflicts.append(
            Conflict(kind, entity, repr(op), message, detail or {})
        )

    for op in patch.ops:
        if isinstance(op, AddVertex):
            existing = work.vertices.get(op.vid)
            if existing is None:
                relocate(op)
            elif attrs_equal(existing.attrs, op.attrs) and existing.interval == op.interval:
                pass  # identical vertex is already present: idempotent
            else:
                conflict(
                    SAME_NAME_DIFFERENT_IDENTITY, f"vertex:{op.vid}", op,
                    f"patch adds vertex {op.vid!r} but the baseline already "
                    "has a different vertex with that name",
                    {
                        "patch_attrs": dict(op.attrs),
                        "baseline_attrs": dict(existing.attrs),
                    },
                )
        elif isinstance(op, RemoveVertex):
            existing = work.vertices.get(op.vid)
            if existing is None:
                pass  # both sides deleted it: same outcome, nothing to do
            elif (
                not attrs_equal(existing.attrs, op.attrs)
                or existing.interval != op.interval
            ):
                conflict(
                    REMOVED_VS_MODIFIED, f"vertex:{op.vid}", op,
                    f"patch removes vertex {op.vid!r} but the baseline "
                    "modified it",
                    {
                        "patch_before_attrs": dict(op.attrs),
                        "baseline_attrs": dict(existing.attrs),
                    },
                )
            else:
                relocate(op)
        elif isinstance(op, RenameVertex):
            if op.old not in work.vertices:
                conflict(
                    RENAME_VS_DELETE, f"vertex:{op.old}", op,
                    f"patch renames {op.old!r} -> {op.new!r} but the "
                    f"baseline deleted {op.old!r}",
                    {"old": op.old, "new": op.new},
                )
            elif op.new in work.vertices:
                conflict(
                    SAME_NAME_DIFFERENT_IDENTITY, f"vertex:{op.new}", op,
                    f"patch renames {op.old!r} -> {op.new!r} but the "
                    "baseline already has a different vertex with that name",
                    {"old": op.old, "new": op.new},
                )
            else:
                relocate(op)
        elif isinstance(op, ChangeVertexAttrs):
            existing = work.vertices.get(op.vid)
            if existing is None:
                conflict(
                    MODIFIED_VS_REMOVED, f"vertex:{op.vid}", op,
                    f"patch modifies vertex {op.vid!r} but the baseline "
                    "deleted it",
                    {"delta": op.delta.to_jsonable()},
                )
                continue
            kept: dict = {}
            for key, (before, after) in op.delta.items():
                current = existing.attrs.get(key, MISSING)
                if value_equal(current, before):
                    kept[key] = (before, after)  # provenance preserved
                elif value_equal(current, after):
                    pass  # baseline already applied this key change
                else:
                    conflict(
                        ATTR_VALUE, f"vertex:{op.vid}", op,
                        f"vertex {op.vid!r} attr {key!r}: patch expects "
                        f"{before!r} -> {after!r} but the baseline has "
                        f"{current!r}",
                        {
                            "key": key,
                            "expected_before": encode_value(before),
                            "wanted_after": encode_value(after),
                            "baseline_value": encode_value(current),
                        },
                    )
            if kept:
                relocate(ChangeVertexAttrs(op.vid, AttrDelta(kept)))
        elif isinstance(op, ChangeVertexInterval):
            existing = work.vertices.get(op.vid)
            if existing is None:
                conflict(
                    MODIFIED_VS_REMOVED, f"vertex:{op.vid}", op,
                    f"patch modifies vertex {op.vid!r} but the baseline "
                    "deleted it",
                )
            elif existing.interval == op.before:
                relocate(op)
            elif existing.interval == op.after:
                pass  # already applied
            else:
                conflict(
                    INTERVAL_VALUE, f"vertex:{op.vid}", op,
                    f"vertex {op.vid!r} interval: patch expects {op.before!r} "
                    f"but the baseline has {existing.interval!r}",
                    {
                        "expected_before": op.before.to_list(),
                        "wanted_after": op.after.to_list(),
                        "baseline_value": existing.interval.to_list(),
                    },
                )
        elif isinstance(op, AddEdge):
            existing = work.edges.get(op.eid)
            if existing is not None:
                if (
                    (existing.src, existing.dst) == (op.src, op.dst)
                    and attrs_equal(existing.attrs, op.attrs)
                    and existing.interval == op.interval
                ):
                    pass  # identical edge already present
                else:
                    conflict(
                        EDGE_IDENTITY_MISMATCH, f"edge:{op.eid}", op,
                        f"patch adds edge {op.eid!r} but the baseline "
                        "already has a different edge with that id",
                    )
                continue
            missing = [
                ep for ep in (op.src, op.dst) if ep not in work.vertices
            ]
            if missing:
                conflict(
                    ENDPOINT_MISSING, f"edge:{op.eid}", op,
                    f"patch adds edge {op.eid!r} but endpoint(s) "
                    f"{missing!r} are absent from the baseline",
                    {"missing_endpoints": missing},
                )
            else:
                relocate(op)
        elif isinstance(op, RemoveEdge):
            existing = work.edges.get(op.eid)
            if existing is None:
                pass  # both sides deleted it
            elif (
                (existing.src, existing.dst) != (op.src, op.dst)
                or not attrs_equal(existing.attrs, op.attrs)
                or existing.interval != op.interval
            ):
                conflict(
                    REMOVED_VS_MODIFIED, f"edge:{op.eid}", op,
                    f"patch removes edge {op.eid!r} but the baseline "
                    "modified it",
                )
            else:
                relocate(op)
        elif isinstance(op, ChangeEdgeAttrs):
            existing = work.edges.get(op.eid)
            if existing is None:
                conflict(
                    MODIFIED_VS_REMOVED, f"edge:{op.eid}", op,
                    f"patch modifies edge {op.eid!r} but the baseline "
                    "deleted it",
                    {"delta": op.delta.to_jsonable()},
                )
                continue
            kept = {}
            for key, (before, after) in op.delta.items():
                current = existing.attrs.get(key, MISSING)
                if value_equal(current, before):
                    kept[key] = (before, after)
                elif value_equal(current, after):
                    pass
                else:
                    conflict(
                        ATTR_VALUE, f"edge:{op.eid}", op,
                        f"edge {op.eid!r} attr {key!r}: patch expects "
                        f"{before!r} -> {after!r} but the baseline has "
                        f"{current!r}",
                        {
                            "key": key,
                            "expected_before": encode_value(before),
                            "wanted_after": encode_value(after),
                            "baseline_value": encode_value(current),
                        },
                    )
            if kept:
                relocate(ChangeEdgeAttrs(op.eid, AttrDelta(kept)))
        elif isinstance(op, ChangeEdgeInterval):
            existing = work.edges.get(op.eid)
            if existing is None:
                conflict(
                    MODIFIED_VS_REMOVED, f"edge:{op.eid}", op,
                    f"patch modifies edge {op.eid!r} but the baseline "
                    "deleted it",
                )
            elif existing.interval == op.before:
                relocate(op)
            elif existing.interval == op.after:
                pass
            else:
                conflict(
                    INTERVAL_VALUE, f"edge:{op.eid}", op,
                    f"edge {op.eid!r} interval: patch expects {op.before!r} "
                    f"but the baseline has {existing.interval!r}",
                    {
                        "expected_before": op.before.to_list(),
                        "wanted_after": op.after.to_list(),
                        "baseline_value": existing.interval.to_list(),
                    },
                )
        elif isinstance(op, ChangeEdgeEndpoints):
            existing = work.edges.get(op.eid)
            if existing is None:
                conflict(
                    MODIFIED_VS_REMOVED, f"edge:{op.eid}", op,
                    f"patch rewires edge {op.eid!r} but the baseline "
                    "deleted it",
                )
                continue
            current = (existing.src, existing.dst)
            if current == tuple(op.after):
                pass  # already rewired
            elif current != tuple(op.before):
                conflict(
                    EDGE_ENDPOINTS, f"edge:{op.eid}", op,
                    f"edge {op.eid!r}: patch expects endpoints "
                    f"{tuple(op.before)!r} but the baseline has "
                    f"{current!r}",
                    {
                        "expected_before": list(op.before),
                        "wanted_after": list(op.after),
                        "baseline_value": list(current),
                    },
                )
            else:
                missing = [ep for ep in op.after if ep not in work.vertices]
                if missing:
                    conflict(
                        ENDPOINT_MISSING, f"edge:{op.eid}", op,
                        f"patch rewires edge {op.eid!r} to endpoint(s) "
                        f"{missing!r} absent from the baseline",
                        {"missing_endpoints": missing},
                    )
                else:
                    relocate(op)
        else:  # pragma: no cover
            raise TypeError(f"replay: unknown op {op!r}")

    rebased = Patch(
        ops=new_ops,
        base_digest=digest_fn(alt_base.canonical()),
        base_hash=alt_base.content_hash(),
        renames=dict(patch.renames),
    )
    if not conflicts:
        # Record the relocated target so apply() verifies the result too.
        relocated = rebased.apply(alt_base, digest_fn)
        assert relocated == work
        rebased.target_digest = digest_fn(relocated.canonical())
        rebased.target_hash = relocated.content_hash()
    return RebaseResult(patch=rebased, conflicts=conflicts)
