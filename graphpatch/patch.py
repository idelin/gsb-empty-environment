"""Patches: reversible, composable, content-verified.

A Patch carries
  * the ordered operation list,
  * a weak digest of the expected base graph (fast pre-filter only),
  * a strong content hash of the expected base graph (authoritative check),
  * the expected target hashes so a successful apply can be verified too.

The weak digest is NEVER trusted on its own: a matching digest with
mismatching content (a digest collision) is rejected.
"""
from __future__ import annotations

import json
from dataclasses import dataclass, field, replace
from typing import Optional

from .model import DEFAULT_DIGEST_FN, DigestFn, Graph
from .ops import (
    MISSING,
    attrs_equal,
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
    op_from_jsonable,
)


class PreconditionError(Exception):
    """Raised when a patch is applied to a base it was not built for."""


def _invert_op(op: Op) -> Op:
    if isinstance(op, AddVertex):
        return RemoveVertex(op.vid, dict(op.attrs), op.interval)
    if isinstance(op, RemoveVertex):
        return AddVertex(op.vid, dict(op.attrs), op.interval)
    if isinstance(op, RenameVertex):
        return RenameVertex(op.new, op.old)
    if isinstance(op, ChangeVertexAttrs):
        return ChangeVertexAttrs(op.vid, op.delta.inverse())
    if isinstance(op, ChangeVertexInterval):
        return ChangeVertexInterval(op.vid, op.after, op.before)
    if isinstance(op, AddEdge):
        return RemoveEdge(op.eid, op.src, op.dst, dict(op.attrs), op.interval)
    if isinstance(op, RemoveEdge):
        return AddEdge(op.eid, op.src, op.dst, dict(op.attrs), op.interval)
    if isinstance(op, ChangeEdgeAttrs):
        return ChangeEdgeAttrs(op.eid, op.delta.inverse())
    if isinstance(op, ChangeEdgeInterval):
        return ChangeEdgeInterval(op.eid, op.after, op.before)
    if isinstance(op, ChangeEdgeEndpoints):
        return ChangeEdgeEndpoints(op.eid, op.after, op.before)
    raise TypeError(f"cannot invert {op!r}")  # pragma: no cover


def _apply_op(g: Graph, op: Op) -> None:
    """Apply one op to g (mutates g), verifying per-op before-images."""
    from .model import Edge, Vertex

    if isinstance(op, AddVertex):
        if op.vid in g.vertices:
            raise PreconditionError(f"add_vertex: {op.vid!r} already exists")
        g.add_vertex(Vertex(op.vid, dict(op.attrs), op.interval))
    elif isinstance(op, RemoveVertex):
        v = g.vertices.get(op.vid)
        if v is None:
            raise PreconditionError(f"remove_vertex: {op.vid!r} is absent")
        if not attrs_equal(v.attrs, op.attrs) or v.interval != op.interval:
            raise PreconditionError(
                f"remove_vertex: {op.vid!r} content mismatch "
                f"(expected attrs={op.attrs!r} interval={op.interval!r}, "
                f"found attrs={v.attrs!r} interval={v.interval!r})"
            )
        g.remove_vertex(op.vid)
    elif isinstance(op, RenameVertex):
        if op.old not in g.vertices:
            raise PreconditionError(f"rename_vertex: {op.old!r} is absent")
        if op.new in g.vertices:
            raise PreconditionError(
                f"rename_vertex: target name {op.new!r} already taken"
            )
        v = g.vertices.pop(op.old)
        g.vertices[op.new] = replace(v, vid=op.new)
        for eid, e in list(g.edges.items()):
            src = op.new if e.src == op.old else e.src
            dst = op.new if e.dst == op.old else e.dst
            if (src, dst) != (e.src, e.dst):
                g.edges[eid] = replace(e, src=src, dst=dst)
    elif isinstance(op, ChangeVertexAttrs):
        v = g.vertices.get(op.vid)
        if v is None:
            raise PreconditionError(f"change_vertex_attrs: {op.vid!r} is absent")
        try:
            new_attrs = op.delta.apply_to(v.attrs, verify=True)
        except ValueError as exc:
            raise PreconditionError(
                f"change_vertex_attrs {op.vid!r}: {exc}"
            ) from exc
        g.vertices[op.vid] = replace(v, attrs=new_attrs)
    elif isinstance(op, ChangeVertexInterval):
        v = g.vertices.get(op.vid)
        if v is None:
            raise PreconditionError(
                f"change_vertex_interval: {op.vid!r} is absent"
            )
        if v.interval != op.before:
            raise PreconditionError(
                f"change_vertex_interval {op.vid!r}: expected interval "
                f"{op.before!r}, found {v.interval!r}"
            )
        g.vertices[op.vid] = replace(v, interval=op.after)
    elif isinstance(op, AddEdge):
        if op.eid in g.edges:
            raise PreconditionError(f"add_edge: {op.eid!r} already exists")
        g.add_edge(Edge(op.eid, op.src, op.dst, dict(op.attrs), op.interval))
    elif isinstance(op, RemoveEdge):
        e = g.edges.get(op.eid)
        if e is None:
            raise PreconditionError(f"remove_edge: {op.eid!r} is absent")
        if (
            (e.src, e.dst) != (op.src, op.dst)
            or not attrs_equal(e.attrs, op.attrs)
            or e.interval != op.interval
        ):
            raise PreconditionError(
                f"remove_edge: {op.eid!r} content mismatch"
            )
        g.remove_edge(op.eid)
    elif isinstance(op, ChangeEdgeAttrs):
        e = g.edges.get(op.eid)
        if e is None:
            raise PreconditionError(f"change_edge_attrs: {op.eid!r} is absent")
        try:
            new_attrs = op.delta.apply_to(e.attrs, verify=True)
        except ValueError as exc:
            raise PreconditionError(
                f"change_edge_attrs {op.eid!r}: {exc}"
            ) from exc
        g.edges[op.eid] = replace(e, attrs=new_attrs)
    elif isinstance(op, ChangeEdgeInterval):
        e = g.edges.get(op.eid)
        if e is None:
            raise PreconditionError(
                f"change_edge_interval: {op.eid!r} is absent"
            )
        if e.interval != op.before:
            raise PreconditionError(
                f"change_edge_interval {op.eid!r}: expected interval "
                f"{op.before!r}, found {e.interval!r}"
            )
        g.edges[op.eid] = replace(e, interval=op.after)
    elif isinstance(op, ChangeEdgeEndpoints):
        e = g.edges.get(op.eid)
        if e is None:
            raise PreconditionError(
                f"change_edge_endpoints: {op.eid!r} is absent"
            )
        if (e.src, e.dst) != tuple(op.before):
            raise PreconditionError(
                f"change_edge_endpoints {op.eid!r}: expected endpoints "
                f"{op.before!r}, found {(e.src, e.dst)!r}"
            )
        src, dst = op.after
        for endpoint in (src, dst):
            if endpoint not in g.vertices:
                raise PreconditionError(
                    f"change_edge_endpoints {op.eid!r}: vertex "
                    f"{endpoint!r} is absent"
                )
        g.edges[op.eid] = replace(e, src=src, dst=dst)
    else:  # pragma: no cover
        raise TypeError(f"unknown op {op!r}")


@dataclass
class Patch:
    """A reversible, composable graph patch."""

    ops: list = field(default_factory=list)
    base_digest: Optional[str] = None   # weak digest of the base (fast filter)
    base_hash: Optional[str] = None     # strong content hash of the base
    target_digest: Optional[str] = None
    target_hash: Optional[str] = None
    renames: dict = field(default_factory=dict)  # rename map used by the diff

    # -- verification -------------------------------------------------------

    def check_base(
        self, graph: Graph, digest_fn: DigestFn = DEFAULT_DIGEST_FN
    ) -> None:
        """Verify `graph` is the base this patch was built from.

        The weak digest is only a fast pre-filter; acceptance always
        requires the strong content hash to match, so a digest collision
        can never smuggle in a wrong baseline.
        """
        if self.base_hash is None:
            return
        canonical = graph.canonical()
        if digest_fn(canonical) != self.base_digest:
            raise PreconditionError(
                "base digest mismatch: patch was built for a different graph"
            )
        if graph.content_hash() != self.base_hash:
            raise PreconditionError(
                "base digest matched but content differs "
                "(digest collision): refusing to apply"
            )

    # -- application ----------------------------------------------------------

    def apply(
        self,
        graph: Graph,
        digest_fn: DigestFn = DEFAULT_DIGEST_FN,
        verify_target: bool = True,
    ) -> Graph:
        """Return a NEW graph with the patch applied; `graph` is untouched.

        Raises PreconditionError on any mismatch; the input graph is never
        mutated, so a rejected patch leaves the original graph unchanged.
        """
        self.check_base(graph, digest_fn)
        g = graph.copy()
        for op in self.ops:
            _apply_op(g, op)
        if verify_target and self.target_hash is not None:
            if g.content_hash() != self.target_hash:
                raise PreconditionError(
                    "patched result does not match the recorded target"
                )
        return g

    # -- algebra --------------------------------------------------------------

    def inverse(self) -> "Patch":
        return Patch(
            ops=[_invert_op(op) for op in reversed(self.ops)],
            base_digest=self.target_digest,
            base_hash=self.target_hash,
            target_digest=self.base_digest,
            target_hash=self.base_hash,
            renames={new: old for old, new in self.renames.items()},
        )

    # -- serialization ----------------------------------------------------------

    def to_jsonable(self) -> dict:
        return {
            "base_digest": self.base_digest,
            "base_hash": self.base_hash,
            "target_digest": self.target_digest,
            "target_hash": self.target_hash,
            "renames": dict(self.renames),
            "ops": [op.to_jsonable() for op in self.ops],
        }

    def to_json(self) -> str:
        return json.dumps(self.to_jsonable(), sort_keys=True, indent=2)

    @staticmethod
    def from_jsonable(data: dict) -> "Patch":
        return Patch(
            ops=[op_from_jsonable(d) for d in data["ops"]],
            base_digest=data.get("base_digest"),
            base_hash=data.get("base_hash"),
            target_digest=data.get("target_digest"),
            target_hash=data.get("target_hash"),
            renames=dict(data.get("renames", {})),
        )

    @staticmethod
    def from_json(text: str) -> "Patch":
        return Patch.from_jsonable(json.loads(text))


# ---------------------------------------------------------------------------
# Composition
# ---------------------------------------------------------------------------
#
# Entity states are keyed by *identity* (the original id of a base vertex,
# or the id at creation for added vertices).  All op-time vertex names are
# resolved through a name index that follows renames, so renames never leak
# into edge endpoint changes and node identity is preserved across
# remove/re-add sequences.


class _VertexState:
    __slots__ = (
        "created", "present", "original", "name",
        "base_attrs", "now_attrs", "base_interval", "now_interval",
    )

    def __init__(self, created, original, name, attrs, interval):
        self.created = created
        self.present = True
        self.original = original
        self.name = name
        self.base_attrs = dict(attrs)
        self.now_attrs = dict(attrs)
        self.base_interval = interval
        self.now_interval = interval


class _EdgeState:
    __slots__ = (
        "created", "present", "eid",
        "base_eps", "now_eps", "base_attrs", "now_attrs",
        "base_interval", "now_interval",
    )

    def __init__(self, created, eid, eps, attrs, interval):
        # eps are tuples of vertex *identities* (original ids)
        self.created = created
        self.present = True
        self.eid = eid
        self.base_eps = tuple(eps)
        self.now_eps = tuple(eps)
        self.base_attrs = dict(attrs)
        self.now_attrs = dict(attrs)
        self.base_interval = interval
        self.now_interval = interval


def compose(first: Patch, second: Patch, base: Graph) -> Patch:
    """Compose two patches over their common base graph.

    ``first`` maps base -> mid, ``second`` maps mid -> target; the result
    maps base -> target.  The base graph is required so that entity
    before-images (node identity, attribute provenance, edge multiplicity)
    are tracked exactly -- even for entities that are first modified and
    later removed.  Composition keeps the earliest before-image and the
    latest after-image per attribute key.
    """
    if first.base_hash is not None and base.content_hash() != first.base_hash:
        raise PreconditionError(
            "compose: supplied base graph does not match the first patch"
        )

    vertices: dict[str, _VertexState] = {
        vid: _VertexState(False, vid, vid, v.attrs, v.interval)
        for vid, v in base.vertices.items()
    }
    edges: dict[str, _EdgeState] = {
        eid: _EdgeState(False, eid, (e.src, e.dst), e.attrs, e.interval)
        for eid, e in base.edges.items()
    }
    name_index: dict[str, str] = {vid: vid for vid in base.vertices}

    def resolve(vid: str) -> str:
        orig = name_index.get(vid)
        if orig is None:
            raise ValueError(f"compose: unknown vertex name {vid!r}")
        st = vertices[orig]
        if not st.present:
            raise ValueError(f"compose: vertex {vid!r} is not present")
        return orig

    def current_name(orig: str) -> str:
        return vertices[orig].name

    for op in list(first.ops) + list(second.ops):
        if isinstance(op, AddVertex):
            if op.vid in name_index and vertices[name_index[op.vid]].present:
                raise ValueError(f"compose: duplicate add of vertex {op.vid!r}")
            existing = vertices.get(op.vid)
            if existing is not None and not existing.present and not existing.created:
                # removed and re-added under the same id: same identity,
                # record as a modification
                existing.present = True
                existing.name = op.vid
                name_index[op.vid] = op.vid
                existing.now_attrs = dict(op.attrs)
                existing.now_interval = op.interval
            else:
                st = _VertexState(True, op.vid, op.vid, op.attrs, op.interval)
                vertices[op.vid] = st
                name_index[op.vid] = op.vid
        elif isinstance(op, RemoveVertex):
            orig = resolve(op.vid)
            vertices[orig].present = False
            del name_index[op.vid]
        elif isinstance(op, RenameVertex):
            orig = resolve(op.old)
            del name_index[op.old]
            name_index[op.new] = orig
            vertices[orig].name = op.new
        elif isinstance(op, ChangeVertexAttrs):
            orig = resolve(op.vid)
            vertices[orig].now_attrs = op.delta.apply_to(
                vertices[orig].now_attrs
            )
        elif isinstance(op, ChangeVertexInterval):
            orig = resolve(op.vid)
            vertices[orig].now_interval = op.after
        elif isinstance(op, AddEdge):
            if op.eid in edges and edges[op.eid].present:
                raise ValueError(f"compose: duplicate add of edge {op.eid!r}")
            eps = (resolve(op.src), resolve(op.dst))
            edges[op.eid] = _EdgeState(
                True, op.eid, eps, op.attrs, op.interval
            )
        elif isinstance(op, RemoveEdge):
            st = edges.get(op.eid)
            if st is None or not st.present:
                raise ValueError(f"compose: unknown edge {op.eid!r}")
            st.present = False
        elif isinstance(op, ChangeEdgeAttrs):
            st = edges.get(op.eid)
            if st is None or not st.present:
                raise ValueError(f"compose: unknown edge {op.eid!r}")
            st.now_attrs = op.delta.apply_to(st.now_attrs)
        elif isinstance(op, ChangeEdgeInterval):
            st = edges.get(op.eid)
            if st is None or not st.present:
                raise ValueError(f"compose: unknown edge {op.eid!r}")
            st.now_interval = op.after
        elif isinstance(op, ChangeEdgeEndpoints):
            st = edges.get(op.eid)
            if st is None or not st.present:
                raise ValueError(f"compose: unknown edge {op.eid!r}")
            st.now_eps = (resolve(op.after[0]), resolve(op.after[1]))
        else:  # pragma: no cover
            raise TypeError(f"compose: unknown op {op!r}")

    ops: list[Op] = []

    # Renames of vertices that survive to the target.  A pre-existing
    # vertex renamed and later removed collapses to a plain removal (the
    # rename never becomes observable).
    renames: dict[str, str] = {}
    for st in vertices.values():
        if not st.created and st.present and st.name != st.original:
            renames[st.original] = st.name

    def apply_renames_to_eps(eps) -> tuple:
        return tuple(renames.get(orig, orig) for orig in eps)

    def emit_endpoint_name(orig: str) -> str:
        st = vertices[orig]
        if not st.present:
            raise ValueError("compose: edge endpoint is absent in target")
        return st.name

    # Emission order keeps the patch applicable: renames, edge removals,
    # vertex removals, vertex additions, edge additions, then changes.
    for old in sorted(renames):
        ops.append(RenameVertex(old, renames[old]))

    for st in sorted(edges.values(), key=lambda s: s.eid):
        if not st.created and not st.present:
            # before-image speaks post-rename names (renames run first)
            eps = apply_renames_to_eps(st.base_eps)
            ops.append(
                RemoveEdge(st.eid, *eps, st.base_attrs, st.base_interval)
            )
    for st in sorted(vertices.values(), key=lambda s: s.original):
        if not st.created and not st.present:
            ops.append(
                RemoveVertex(st.original, st.base_attrs, st.base_interval)
            )
    for st in sorted(vertices.values(), key=lambda s: s.name):
        if st.created and st.present:
            ops.append(AddVertex(st.name, st.now_attrs, st.now_interval))
    for st in sorted(edges.values(), key=lambda s: s.eid):
        if st.created and st.present:
            eps = tuple(emit_endpoint_name(o) for o in st.now_eps)
            ops.append(
                AddEdge(st.eid, *eps, st.now_attrs, st.now_interval)
            )
    for st in sorted(vertices.values(), key=lambda s: s.name):
        if not st.created and st.present:
            delta = AttrDelta.between(st.base_attrs, st.now_attrs)
            if not delta.is_empty():
                ops.append(ChangeVertexAttrs(st.name, delta))
            if st.base_interval != st.now_interval:
                ops.append(
                    ChangeVertexInterval(st.name, st.base_interval, st.now_interval)
                )
    for st in sorted(edges.values(), key=lambda s: s.eid):
        if not st.created and st.present:
            # Endpoint changes beyond what the emitted renames already
            # explain; before-images speak post-rename names.
            base_after_renames = apply_renames_to_eps(st.base_eps)
            now_names = tuple(emit_endpoint_name(o) for o in st.now_eps)
            if base_after_renames != now_names:
                ops.append(
                    ChangeEdgeEndpoints(st.eid, base_after_renames, now_names)
                )
            delta = AttrDelta.between(st.base_attrs, st.now_attrs)
            if not delta.is_empty():
                ops.append(ChangeEdgeAttrs(st.eid, delta))
            if st.base_interval != st.now_interval:
                ops.append(
                    ChangeEdgeInterval(st.eid, st.base_interval, st.now_interval)
                )

    return Patch(
        ops=ops,
        base_digest=first.base_digest,
        base_hash=first.base_hash,
        target_digest=second.target_digest,
        target_hash=second.target_hash,
        renames=renames,
    )
