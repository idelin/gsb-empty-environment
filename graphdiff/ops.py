"""Atomic patch operations. Every op carries enough information (old and new
values) to be inverted without access to either graph, and checks its own
preconditions when applied."""
from __future__ import annotations

from typing import Any

from .model import Graph, Vertex, Edge, Interval, MISSING, values_equal, attrs_equal


class PreconditionError(RuntimeError):
    """An op's local precondition did not hold on the graph it was applied to."""


def _enc(value: Any) -> Any:
    return {"__missing__": True} if value is MISSING else {"v": value}


def _dec(data: Any) -> Any:
    return MISSING if "__missing__" in data else data["v"]


class Op:
    kind = "op"

    def apply(self, g: Graph) -> None:  # pragma: no cover - abstract
        raise NotImplementedError

    def inverse(self) -> "Op":  # pragma: no cover - abstract
        raise NotImplementedError

    def to_data(self) -> dict:  # pragma: no cover - abstract
        raise NotImplementedError


class RenameNode(Op):
    kind = "rename_node"

    def __init__(self, old: str, new: str):
        self.old, self.new = old, new

    def apply(self, g: Graph) -> None:
        if self.old not in g.vertices:
            raise PreconditionError(f"rename: no vertex {self.old!r}")
        if self.new in g.vertices:
            raise PreconditionError(f"rename: target {self.new!r} already exists")
        v = g.vertices.pop(self.old)
        v.id = self.new
        g.vertices[self.new] = v
        for e in g.edges.values():
            if e.src == self.old:
                e.src = self.new
            if e.dst == self.old:
                e.dst = self.new

    def inverse(self) -> "Op":
        return RenameNode(self.new, self.old)

    def to_data(self):
        return {"op": self.kind, "old": self.old, "new": self.new}

    def __repr__(self):
        return f"RenameNode({self.old!r} -> {self.new!r})"


class AddNode(Op):
    kind = "add_node"

    def __init__(self, vid: str, attrs: dict, interval: Interval):
        self.vid, self.attrs, self.interval = vid, dict(attrs), interval

    def apply(self, g: Graph) -> None:
        if self.vid in g.vertices:
            raise PreconditionError(f"add_node: {self.vid!r} already exists")
        g.vertices[self.vid] = Vertex(self.vid, dict(self.attrs), self.interval)

    def inverse(self) -> "Op":
        return DelNode(self.vid, self.attrs, self.interval)

    def to_data(self):
        return {"op": self.kind, "id": self.vid, "attrs": self.attrs,
                "interval": self.interval.to_data()}

    def __repr__(self):
        return f"AddNode({self.vid!r}, attrs={self.attrs!r}, iv={self.interval!r})"


class DelNode(Op):
    kind = "del_node"

    def __init__(self, vid: str, attrs: dict, interval: Interval):
        # stores the full vertex so the inverse can restore it (provenance)
        self.vid, self.attrs, self.interval = vid, dict(attrs), interval

    def apply(self, g: Graph) -> None:
        v = g.vertices.get(self.vid)
        if v is None:
            raise PreconditionError(f"del_node: no vertex {self.vid!r}")
        if not attrs_equal(v.attrs, self.attrs) or v.interval != self.interval:
            raise PreconditionError(f"del_node: vertex {self.vid!r} content mismatch")
        for e in g.edges.values():
            if e.src == self.vid or e.dst == self.vid:
                raise PreconditionError(
                    f"del_node: vertex {self.vid!r} still has incident edges")
        del g.vertices[self.vid]

    def inverse(self) -> "Op":
        return AddNode(self.vid, self.attrs, self.interval)

    def to_data(self):
        return {"op": self.kind, "id": self.vid, "attrs": self.attrs,
                "interval": self.interval.to_data()}

    def __repr__(self):
        return f"DelNode({self.vid!r})"


class AddEdge(Op):
    kind = "add_edge"

    def __init__(self, eid: str, src: str, dst: str, attrs: dict, interval: Interval):
        self.eid, self.src, self.dst = eid, src, dst
        self.attrs, self.interval = dict(attrs), interval

    def apply(self, g: Graph) -> None:
        if self.eid in g.edges:
            raise PreconditionError(f"add_edge: {self.eid!r} already exists")
        for ep in (self.src, self.dst):
            if ep not in g.vertices:
                raise PreconditionError(f"add_edge: missing endpoint {ep!r}")
        g.edges[self.eid] = Edge(self.eid, self.src, self.dst,
                                 dict(self.attrs), self.interval)

    def inverse(self) -> "Op":
        return DelEdge(self.eid, self.src, self.dst, self.attrs, self.interval)

    def to_data(self):
        return {"op": self.kind, "id": self.eid, "src": self.src, "dst": self.dst,
                "attrs": self.attrs, "interval": self.interval.to_data()}

    def __repr__(self):
        return f"AddEdge({self.eid!r}: {self.src!r}->{self.dst!r})"


class DelEdge(Op):
    kind = "del_edge"

    def __init__(self, eid: str, src: str, dst: str, attrs: dict, interval: Interval):
        # stores the full edge so the inverse can restore it (provenance)
        self.eid, self.src, self.dst = eid, src, dst
        self.attrs, self.interval = dict(attrs), interval

    def apply(self, g: Graph) -> None:
        e = g.edges.get(self.eid)
        if e is None:
            raise PreconditionError(f"del_edge: no edge {self.eid!r}")
        if (e.src, e.dst) != (self.src, self.dst) \
                or not attrs_equal(e.attrs, self.attrs) \
                or e.interval != self.interval:
            raise PreconditionError(f"del_edge: edge {self.eid!r} content mismatch")
        del g.edges[self.eid]

    def inverse(self) -> "Op":
        return AddEdge(self.eid, self.src, self.dst, self.attrs, self.interval)

    def to_data(self):
        return {"op": self.kind, "id": self.eid, "src": self.src, "dst": self.dst,
                "attrs": self.attrs, "interval": self.interval.to_data()}

    def __repr__(self):
        return f"DelEdge({self.eid!r})"


class ReconnectEdge(Op):
    kind = "reconnect_edge"

    def __init__(self, eid: str, old_src: str, old_dst: str,
                 new_src: str, new_dst: str):
        self.eid = eid
        self.old_src, self.old_dst = old_src, old_dst
        self.new_src, self.new_dst = new_src, new_dst

    def apply(self, g: Graph) -> None:
        e = g.edges.get(self.eid)
        if e is None:
            raise PreconditionError(f"reconnect: no edge {self.eid!r}")
        if (e.src, e.dst) != (self.old_src, self.old_dst):
            raise PreconditionError(f"reconnect: edge {self.eid!r} endpoints mismatch")
        for ep in (self.new_src, self.new_dst):
            if ep not in g.vertices:
                raise PreconditionError(f"reconnect: missing endpoint {ep!r}")
        e.src, e.dst = self.new_src, self.new_dst

    def inverse(self) -> "Op":
        return ReconnectEdge(self.eid, self.new_src, self.new_dst,
                             self.old_src, self.old_dst)

    def to_data(self):
        return {"op": self.kind, "id": self.eid, "old_src": self.old_src,
                "old_dst": self.old_dst, "new_src": self.new_src,
                "new_dst": self.new_dst}

    def __repr__(self):
        return (f"ReconnectEdge({self.eid!r}: {self.old_src!r},{self.old_dst!r}"
                f" -> {self.new_src!r},{self.new_dst!r})")


class SetAttr(Op):
    """Set or delete one attribute key on a vertex or edge.

    old/new may be MISSING (key absent). Provenance: `old` is the value in the
    patch's base version, preserved through composition."""

    kind = "set_attr"

    def __init__(self, entity: str, eid: str, key: str, old: Any, new: Any):
        assert entity in ("node", "edge")
        assert not (old is MISSING and new is MISSING)
        self.entity, self.eid, self.key = entity, eid, key
        self.old, self.new = old, new

    def _attrs(self, g: Graph) -> dict:
        holder = g.vertices.get(self.eid) if self.entity == "node" \
            else g.edges.get(self.eid)
        if holder is None:
            raise PreconditionError(
                f"set_attr: no {self.entity} {self.eid!r}")
        return holder.attrs

    def apply(self, g: Graph) -> None:
        attrs = self._attrs(g)
        if not values_equal(attrs.get(self.key, MISSING), self.old):
            raise PreconditionError(
                f"set_attr: {self.entity} {self.eid!r} key {self.key!r} "
                f"is {attrs.get(self.key, MISSING)!r}, expected {self.old!r}")
        if self.new is MISSING:
            del attrs[self.key]
        else:
            attrs[self.key] = self.new

    def inverse(self) -> "Op":
        return SetAttr(self.entity, self.eid, self.key, self.new, self.old)

    def to_data(self):
        return {"op": self.kind, "entity": self.entity, "id": self.eid,
                "key": self.key, "old": _enc(self.old), "new": _enc(self.new)}

    def __repr__(self):
        return (f"SetAttr({self.entity} {self.eid!r}.{self.key!r}: "
                f"{self.old!r} -> {self.new!r})")


class SetInterval(Op):
    kind = "set_interval"

    def __init__(self, entity: str, eid: str, old: Interval, new: Interval):
        assert entity in ("node", "edge")
        self.entity, self.eid, self.old, self.new = entity, eid, old, new

    def apply(self, g: Graph) -> None:
        holder = g.vertices.get(self.eid) if self.entity == "node" \
            else g.edges.get(self.eid)
        if holder is None:
            raise PreconditionError(f"set_interval: no {self.entity} {self.eid!r}")
        if holder.interval != self.old:
            raise PreconditionError(
                f"set_interval: {self.entity} {self.eid!r} interval mismatch")
        holder.interval = self.new

    def inverse(self) -> "Op":
        return SetInterval(self.entity, self.eid, self.new, self.old)

    def to_data(self):
        return {"op": self.kind, "entity": self.entity, "id": self.eid,
                "old": self.old.to_data(), "new": self.new.to_data()}

    def __repr__(self):
        return f"SetInterval({self.entity} {self.eid!r}: {self.old!r} -> {self.new!r})"


def op_from_data(d: dict) -> Op:
    k = d["op"]
    if k == "rename_node":
        return RenameNode(d["old"], d["new"])
    if k == "add_node":
        return AddNode(d["id"], d["attrs"], Interval.from_data(d["interval"]))
    if k == "del_node":
        return DelNode(d["id"], d["attrs"], Interval.from_data(d["interval"]))
    if k == "add_edge":
        return AddEdge(d["id"], d["src"], d["dst"], d["attrs"],
                       Interval.from_data(d["interval"]))
    if k == "del_edge":
        return DelEdge(d["id"], d["src"], d["dst"], d["attrs"],
                       Interval.from_data(d["interval"]))
    if k == "reconnect_edge":
        return ReconnectEdge(d["id"], d["old_src"], d["old_dst"],
                             d["new_src"], d["new_dst"])
    if k == "set_attr":
        return SetAttr(d["entity"], d["id"], d["key"], _dec(d["old"]), _dec(d["new"]))
    if k == "set_interval":
        return SetInterval(d["entity"], d["id"],
                           Interval.from_data(d["old"]), Interval.from_data(d["new"]))
    raise ValueError(f"unknown op {k!r}")
