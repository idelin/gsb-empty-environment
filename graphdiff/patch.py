"""Patch: an ordered op list plus base/target version fingerprints.

Precondition verification is two-stage:
1. fast digest check (reject obviously wrong baselines);
2. authoritative content re-check of the canonical serialization, so a
   forged/colliding digest can never make a wrong baseline pass.
"""
from __future__ import annotations

import json
from typing import Optional

from . import hashing
from .model import Graph, values_equal, attrs_equal
from .ops import (
    Op, MISSING, RenameNode, AddNode, DelNode, AddEdge, DelEdge,
    ReconnectEdge, SetAttr, SetInterval, op_from_data,
)


class BaseMismatchError(RuntimeError):
    """Digest of the graph does not match the patch's base fingerprint."""


class ContentMismatchError(BaseMismatchError):
    """Digest matched but canonical content differs (collision / forgery)."""


class ComposeError(ValueError):
    pass


def _fingerprint(g: Graph) -> tuple[str, str]:
    content = g.canonical()
    return hashing.digest(content), content


class Patch:
    def __init__(self, ops: list[Op], base_digest: str, base_content: str,
                 target_digest: str, target_content: str):
        self.ops = list(ops)
        self.base_digest = base_digest
        self.base_content = base_content
        self.target_digest = target_digest
        self.target_content = target_content

    # -- construction --------------------------------------------------------
    @staticmethod
    def between(base: Graph, target: Graph, ops: list[Op]) -> "Patch":
        bd, bc = _fingerprint(base)
        td, tc = _fingerprint(target)
        return Patch(ops, bd, bc, td, tc)

    # -- verification --------------------------------------------------------
    def _check(self, g: Graph, digest_expected: str, content_expected: str,
               label: str) -> None:
        content = g.canonical()
        if hashing.digest(content) != digest_expected:
            raise BaseMismatchError(
                f"{label} digest mismatch: patch expects "
                f"{digest_expected[:12]}..., graph has "
                f"{hashing.digest(content)[:12]}...")
        if content != content_expected:
            raise ContentMismatchError(
                f"{label} digest matched but content differs; refusing to "
                f"accept a wrong baseline on digest alone")

    def check_base(self, g: Graph) -> None:
        self._check(g, self.base_digest, self.base_content, "base")

    # -- application ---------------------------------------------------------
    def apply(self, g: Graph, verify: bool = True) -> Graph:
        """Return a NEW graph; the input is never mutated, so a rejected
        baseline is guaranteed to be left unchanged."""
        if verify:
            self.check_base(g)
        new = g.copy()
        for op in self.ops:
            op.apply(new)
        if verify:
            self._check(new, self.target_digest, self.target_content, "target")
        return new

    # -- algebra -------------------------------------------------------------
    def inverse(self) -> "Patch":
        return Patch(
            [op.inverse() for op in reversed(self.ops)],
            self.target_digest, self.target_content,
            self.base_digest, self.base_content,
        )

    def __len__(self):
        return len(self.ops)

    def __repr__(self):
        return f"Patch({len(self.ops)} ops)"

    # -- serialization -------------------------------------------------------
    def to_data(self) -> dict:
        return {
            "base_digest": self.base_digest,
            "base_content": json.loads(self.base_content),
            "target_digest": self.target_digest,
            "target_content": json.loads(self.target_content),
            "ops": [op.to_data() for op in self.ops],
        }

    def to_json(self) -> str:
        return json.dumps(self.to_data(), sort_keys=True, indent=2)

    @staticmethod
    def from_data(d: dict) -> "Patch":
        return Patch(
            [op_from_data(o) for o in d["ops"]],
            d["base_digest"], json.dumps(d["base_content"], sort_keys=True,
                                         separators=(",", ":")),
            d["target_digest"], json.dumps(d["target_content"], sort_keys=True,
                                           separators=(",", ":")),
        )

    @staticmethod
    def from_json(text: str) -> "Patch":
        return Patch.from_data(json.loads(text))


# ---------------------------------------------------------------------------
# Composition: concatenate then simplify adjacent op pairs to a fixpoint.
# Every rewrite rule is semantics-preserving and keeps base-side provenance
# (oldest `old` values) and entity identity (string IDs).
# ---------------------------------------------------------------------------

def _merge_pair(a: Op, b: Op) -> Optional[list[Op]]:
    """Try to fuse adjacent ops a,b. Returns replacement list, or None."""
    # rename chains: a->b then b->c  =>  a->c (or nothing for a->b->a)
    if isinstance(a, RenameNode) and isinstance(b, RenameNode) and a.new == b.old:
        return [] if a.old == b.new else [RenameNode(a.old, b.new)]

    # attribute writes on the same key: keep oldest old, newest new
    if (isinstance(a, SetAttr) and isinstance(b, SetAttr)
            and (a.entity, a.eid, a.key) == (b.entity, b.eid, b.key)
            and values_equal(a.new, b.old)):
        if values_equal(a.old, b.new):
            return []
        return [SetAttr(a.entity, a.eid, a.key, a.old, b.new)]

    # interval writes
    if (isinstance(a, SetInterval) and isinstance(b, SetInterval)
            and (a.entity, a.eid) == (b.entity, b.eid) and a.new == b.old):
        if a.old == b.new:
            return []
        return [SetInterval(a.entity, a.eid, a.old, b.new)]

    # add then delete the same vertex/edge: cancel (state must match)
    if isinstance(a, AddNode) and isinstance(b, DelNode) and a.vid == b.vid \
            and attrs_equal(a.attrs, b.attrs) and a.interval == b.interval:
        return []
    if isinstance(a, AddEdge) and isinstance(b, DelEdge) and a.eid == b.eid \
            and (a.src, a.dst) == (b.src, b.dst) \
            and attrs_equal(a.attrs, b.attrs) and a.interval == b.interval:
        return []

    # fold attribute/interval writes into a preceding Add
    if isinstance(a, AddNode) and isinstance(b, SetAttr) \
            and b.entity == "node" and a.vid == b.eid:
        attrs = dict(a.attrs)
        if b.new is MISSING:
            attrs.pop(b.key, None)
        else:
            attrs[b.key] = b.new
        return [AddNode(a.vid, attrs, a.interval)]
    if isinstance(a, AddNode) and isinstance(b, SetInterval) \
            and b.entity == "node" and a.vid == b.eid:
        return [AddNode(a.vid, a.attrs, b.new)]
    if isinstance(a, AddEdge) and isinstance(b, SetAttr) \
            and b.entity == "edge" and a.eid == b.eid:
        attrs = dict(a.attrs)
        if b.new is MISSING:
            attrs.pop(b.key, None)
        else:
            attrs[b.key] = b.new
        return [AddEdge(a.eid, a.src, a.dst, attrs, a.interval)]
    if isinstance(a, AddEdge) and isinstance(b, SetInterval) \
            and b.entity == "edge" and a.eid == b.eid:
        return [AddEdge(a.eid, a.src, a.dst, a.attrs, b.new)]

    # fold reconnects into a preceding AddEdge / merge reconnect chains
    if isinstance(a, AddEdge) and isinstance(b, ReconnectEdge) and a.eid == b.eid \
            and (a.src, a.dst) == (b.old_src, b.old_dst):
        return [AddEdge(a.eid, b.new_src, b.new_dst, a.attrs, a.interval)]
    if isinstance(a, ReconnectEdge) and isinstance(b, ReconnectEdge) \
            and a.eid == b.eid and (a.new_src, a.new_dst) == (b.old_src, b.old_dst):
        if (a.old_src, a.old_dst) == (b.new_src, b.new_dst):
            return []
        return [ReconnectEdge(a.eid, a.old_src, a.old_dst, b.new_src, b.new_dst)]

    # reconnect then delete: delete remembers the base-side endpoints
    if isinstance(a, ReconnectEdge) and isinstance(b, DelEdge) and a.eid == b.eid \
            and (b.src, b.dst) == (a.new_src, a.new_dst):
        return [DelEdge(a.eid, a.old_src, a.old_dst, b.attrs, b.interval)]

    # delete then re-add the same vertex id: identity kept, becomes a modify
    if isinstance(a, DelNode) and isinstance(b, AddNode) and a.vid == b.vid:
        return _modify_ops("node", a.vid, a.attrs, b.attrs, a.interval, b.interval)
    if isinstance(a, DelEdge) and isinstance(b, AddEdge) and a.eid == b.eid \
            and (a.src, a.dst) == (b.src, b.dst):
        return _modify_ops("edge", a.eid, a.attrs, b.attrs, a.interval, b.interval)

    return None


def _modify_ops(entity: str, eid: str, old_attrs: dict, new_attrs: dict,
                old_iv, new_iv) -> list[Op]:
    ops: list[Op] = []
    for key in sorted(old_attrs.keys() | new_attrs.keys()):
        o, n = old_attrs.get(key, MISSING), new_attrs.get(key, MISSING)
        if not values_equal(o, n):
            ops.append(SetAttr(entity, eid, key, o, n))
    if old_iv != new_iv:
        ops.append(SetInterval(entity, eid, old_iv, new_iv))
    return ops


def _rw(op: Op) -> tuple[set, set]:
    """Read/write resource sets of an op, used for commutativity checks.
    Resources: ("n", id) node, ("e", id) edge, ("e", "*") all edges,
    ("p", id) incidence set of a node."""
    if isinstance(op, RenameNode):
        r = {("n", op.old), ("n", op.new), ("e", "*")}
        return r, set(r)
    if isinstance(op, AddNode):
        return {("n", op.vid)}, {("n", op.vid)}
    if isinstance(op, DelNode):
        return {("n", op.vid), ("p", op.vid)}, {("n", op.vid)}
    if isinstance(op, AddEdge):
        return ({("e", op.eid), ("n", op.src), ("n", op.dst)},
                {("e", op.eid), ("p", op.src), ("p", op.dst)})
    if isinstance(op, DelEdge):
        return {("e", op.eid)}, {("e", op.eid), ("p", op.src), ("p", op.dst)}
    if isinstance(op, ReconnectEdge):
        return ({("e", op.eid), ("n", op.new_src), ("n", op.new_dst)},
                {("e", op.eid), ("p", op.old_src), ("p", op.old_dst),
                 ("p", op.new_src), ("p", op.new_dst)})
    if isinstance(op, (SetAttr, SetInterval)):
        return {(op.entity[0], op.eid)}, {(op.entity[0], op.eid)}
    return {("*", "*")}, {("*", "*")}  # unknown ops commute with nothing


def _clash(s1: set, s2: set) -> bool:
    if s1 & s2:
        return True
    wild1 = {k for k, v in s1 if v == "*"}
    wild2 = {k for k, v in s2 if v == "*"}
    if wild1 and any(k in wild1 for k, _ in s2):
        return True
    if wild2 and any(k in wild2 for k, _ in s1):
        return True
    return False


def _commutes(a: Op, b: Op) -> bool:
    ra, wa = _rw(a)
    rb, wb = _rw(b)
    return not _clash(wa, rb | wb) and not _clash(wb, ra | wa)


def simplify(ops: list[Op]) -> list[Op]:
    """Merge op pairs to a fixpoint. Non-adjacent pairs may merge when the
    first op commutes with everything in between (sound reordering)."""
    ops = list(ops)
    changed = True
    while changed:
        changed = False
        for i in range(len(ops)):
            if i >= len(ops):
                break
            for j in range(i + 1, len(ops)):
                merged = _merge_pair(ops[i], ops[j])
                if merged is not None and (
                        j == i + 1
                        or all(_commutes(ops[i], ops[k])
                               for k in range(i + 1, j))):
                    # intermediates stay; the fused result takes j's place
                    ops[i:j + 1] = ops[i + 1:j] + merged
                    changed = True
                    break
                if not _commutes(ops[i], ops[j]):
                    break  # ops[i] cannot move past ops[j]
            if changed:
                break
    return ops


def compose(p1: Patch, p2: Patch) -> Patch:
    """p1: A->B, p2: B->C  =>  A->C. The intermediate version must match
    exactly (digest AND content)."""
    if p1.target_digest != p2.base_digest or p1.target_content != p2.base_content:
        raise ComposeError("p1 target version does not match p2 base version")
    return Patch(simplify(p1.ops + p2.ops),
                 p1.base_digest, p1.base_content,
                 p2.target_digest, p2.target_content)
