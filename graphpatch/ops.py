"""Patch operations.
Attribute changes are stored per key as (before, after) pairs so that
composition keeps the *original* base value (attribute provenance) and
inversion can restore it exactly.  A key absent on one side is represented
with the MISSING sentinel.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Iterator, Optional

from .model import Interval


class _Missing:
    _instance: Optional["_Missing"] = None

    def __new__(cls) -> "_Missing":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "MISSING"

    def __reduce__(self):
        return (_Missing, ())


MISSING = _Missing()


def _canon_value(value):
    import json

    return json.dumps(value, sort_keys=True, separators=(",", ":"))


def value_equal(a, b) -> bool:
    """Type-sensitive value equality (1 != 1.0 != True), MISSING-aware."""
    if a is MISSING or b is MISSING:
        return a is b
    return _canon_value(a) == _canon_value(b)


def attrs_equal(x: dict, y: dict) -> bool:
    if set(x) != set(y):
        return False
    return all(value_equal(x[k], y[k]) for k in x)

_MISSING_TAG = {"$__missing__": True}


def encode_value(value: Any) -> Any:
    return _MISSING_TAG if value is MISSING else value


def decode_value(value: Any) -> Any:
    if value == _MISSING_TAG:
        return MISSING
    return value


class AttrDelta:
    """Per-key attribute change set with provenance.

    changes: {key: (before, after)}; either side may be MISSING when the key
    is added or removed.  Composition keeps the earliest ``before`` and the
    latest ``after``; inversion swaps them.
    """

    __slots__ = ("_changes",)

    def __init__(self, changes: Optional[dict] = None) -> None:
        self._changes: dict[str, tuple] = dict(changes or {})

    # -- constructors -----------------------------------------------------

    @staticmethod
    def between(before: dict, after: dict) -> "AttrDelta":
        changes: dict[str, tuple] = {}
        for key in sorted(set(before) | set(after)):
            b = before.get(key, MISSING)
            a = after.get(key, MISSING)
            if not value_equal(b, a):
                changes[key] = (b, a)
        return AttrDelta(changes)

    # -- accessors ----------------------------------------------------------

    def is_empty(self) -> bool:
        return not self._changes

    def keys(self):
        return self._changes.keys()

    def items(self):
        return self._changes.items()

    def before(self, key: str) -> Any:
        return self._changes[key][0]

    def after(self, key: str) -> Any:
        return self._changes[key][1]

    def __len__(self) -> int:
        return len(self._changes)

    # -- algebra ------------------------------------------------------------

    def apply_to(self, attrs: dict, *, verify: bool = False) -> dict:
        new = dict(attrs)
        for key, (before, after) in self._changes.items():
            if verify:
                current = new.get(key, MISSING)
                if not value_equal(current, before):
                    raise ValueError(
                        f"attr {key!r}: expected {before!r}, found {current!r}"
                    )
            if after is MISSING:
                new.pop(key, None)
            else:
                new[key] = after
        return new

    def inverse(self) -> "AttrDelta":
        return AttrDelta({k: (a, b) for k, (b, a) in self._changes.items()})

    def then(self, nxt: "AttrDelta") -> "AttrDelta":
        """Compose: self applied first, then nxt.  Provenance preserved."""
        merged: dict[str, tuple] = {}
        for key in sorted(set(self._changes) | set(nxt._changes)):
            if key in self._changes:
                before = self._changes[key][0]
            else:
                before = nxt._changes[key][0]
            if key in nxt._changes:
                after = nxt._changes[key][1]
            else:
                after = self._changes[key][1]
            if not value_equal(before, after):
                merged[key] = (before, after)
        return AttrDelta(merged)

    # -- (de)serialization ----------------------------------------------------

    def to_jsonable(self) -> dict:
        return {
            k: [encode_value(b), encode_value(a)]
            for k, (b, a) in sorted(self._changes.items())
        }

    @staticmethod
    def from_jsonable(data: dict) -> "AttrDelta":
        return AttrDelta(
            {k: (decode_value(b), decode_value(a)) for k, (b, a) in data.items()}
        )

    def __eq__(self, other: object) -> bool:
        return isinstance(other, AttrDelta) and self._changes == other._changes

    def __repr__(self) -> str:
        inner = ", ".join(
            f"{k}: {b!r} -> {a!r}" for k, (b, a) in sorted(self._changes.items())
        )
        return f"AttrDelta({{{inner}}})"


# ---------------------------------------------------------------------------
# Operations
# ---------------------------------------------------------------------------


class Op:
    """Base class of all patch operations."""

    def to_jsonable(self) -> dict:  # pragma: no cover - overridden
        raise NotImplementedError


def _interval_to(pair_interval: Interval) -> list:
    return pair_interval.to_list()


@dataclass(frozen=True)
class AddVertex(Op):
    vid: str
    attrs: dict = field(default_factory=dict)
    interval: Interval = Interval()

    def to_jsonable(self) -> dict:
        return {
            "op": "add_vertex",
            "vid": self.vid,
            "attrs": self.attrs,
            "interval": _interval_to(self.interval),
        }


@dataclass(frozen=True)
class RemoveVertex(Op):
    vid: str
    attrs: dict = field(default_factory=dict)  # before-image, for inversion
    interval: Interval = Interval()

    def to_jsonable(self) -> dict:
        return {
            "op": "remove_vertex",
            "vid": self.vid,
            "attrs": self.attrs,
            "interval": _interval_to(self.interval),
        }


@dataclass(frozen=True)
class RenameVertex(Op):
    old: str
    new: str

    def to_jsonable(self) -> dict:
        return {"op": "rename_vertex", "old": self.old, "new": self.new}


@dataclass(frozen=True)
class ChangeVertexAttrs(Op):
    vid: str
    delta: AttrDelta = field(default_factory=AttrDelta)

    def to_jsonable(self) -> dict:
        return {
            "op": "change_vertex_attrs",
            "vid": self.vid,
            "delta": self.delta.to_jsonable(),
        }


@dataclass(frozen=True)
class ChangeVertexInterval(Op):
    vid: str
    before: Interval = Interval()
    after: Interval = Interval()

    def to_jsonable(self) -> dict:
        return {
            "op": "change_vertex_interval",
            "vid": self.vid,
            "before": _interval_to(self.before),
            "after": _interval_to(self.after),
        }


@dataclass(frozen=True)
class AddEdge(Op):
    eid: str
    src: str = ""
    dst: str = ""
    attrs: dict = field(default_factory=dict)
    interval: Interval = Interval()

    def to_jsonable(self) -> dict:
        return {
            "op": "add_edge",
            "eid": self.eid,
            "src": self.src,
            "dst": self.dst,
            "attrs": self.attrs,
            "interval": _interval_to(self.interval),
        }


@dataclass(frozen=True)
class RemoveEdge(Op):
    eid: str
    src: str = ""  # before-image, for inversion
    dst: str = ""
    attrs: dict = field(default_factory=dict)
    interval: Interval = Interval()

    def to_jsonable(self) -> dict:
        return {
            "op": "remove_edge",
            "eid": self.eid,
            "src": self.src,
            "dst": self.dst,
            "attrs": self.attrs,
            "interval": _interval_to(self.interval),
        }


@dataclass(frozen=True)
class ChangeEdgeAttrs(Op):
    eid: str
    delta: AttrDelta = field(default_factory=AttrDelta)

    def to_jsonable(self) -> dict:
        return {
            "op": "change_edge_attrs",
            "eid": self.eid,
            "delta": self.delta.to_jsonable(),
        }


@dataclass(frozen=True)
class ChangeEdgeInterval(Op):
    eid: str
    before: Interval = Interval()
    after: Interval = Interval()

    def to_jsonable(self) -> dict:
        return {
            "op": "change_edge_interval",
            "eid": self.eid,
            "before": _interval_to(self.before),
            "after": _interval_to(self.after),
        }


@dataclass(frozen=True)
class ChangeEdgeEndpoints(Op):
    eid: str
    before: tuple = ("", "")  # (src, dst)
    after: tuple = ("", "")

    def to_jsonable(self) -> dict:
        return {
            "op": "change_edge_endpoints",
            "eid": self.eid,
            "before": list(self.before),
            "after": list(self.after),
        }


_OP_FROM_JSON = {}


def _register(cls, tag, builder):
    _OP_FROM_JSON[tag] = builder


_register(AddVertex, "add_vertex", lambda d: AddVertex(
    d["vid"], d.get("attrs", {}), Interval.from_list(d["interval"])))
_register(RemoveVertex, "remove_vertex", lambda d: RemoveVertex(
    d["vid"], d.get("attrs", {}), Interval.from_list(d["interval"])))
_register(RenameVertex, "rename_vertex", lambda d: RenameVertex(
    d["old"], d["new"]))
_register(ChangeVertexAttrs, "change_vertex_attrs", lambda d: ChangeVertexAttrs(
    d["vid"], AttrDelta.from_jsonable(d["delta"])))
_register(ChangeVertexInterval, "change_vertex_interval", lambda d: ChangeVertexInterval(
    d["vid"], Interval.from_list(d["before"]), Interval.from_list(d["after"])))
_register(AddEdge, "add_edge", lambda d: AddEdge(
    d["eid"], d["src"], d["dst"], d.get("attrs", {}), Interval.from_list(d["interval"])))
_register(RemoveEdge, "remove_edge", lambda d: RemoveEdge(
    d["eid"], d["src"], d["dst"], d.get("attrs", {}), Interval.from_list(d["interval"])))
_register(ChangeEdgeAttrs, "change_edge_attrs", lambda d: ChangeEdgeAttrs(
    d["eid"], AttrDelta.from_jsonable(d["delta"])))
_register(ChangeEdgeInterval, "change_edge_interval", lambda d: ChangeEdgeInterval(
    d["eid"], Interval.from_list(d["before"]), Interval.from_list(d["after"])))
_register(ChangeEdgeEndpoints, "change_edge_endpoints", lambda d: ChangeEdgeEndpoints(
    d["eid"], tuple(d["before"]), tuple(d["after"])))


def op_from_jsonable(data: dict) -> Op:
    return _OP_FROM_JSON[data["op"]](data)
