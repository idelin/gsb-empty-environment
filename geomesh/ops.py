"""Transactional mesh operations with journaling and reversible patches.

Public operations (move_vertex / split_edge / merge_edges) run inside a
Transaction. Every primitive mutation is journaled as a pair of structured
entries (forward + inverse). If any step fails, the whole transaction is
rolled back to its exact prior state. On commit, the journal becomes a
Patch whose forward/inverse entry lists restore IDs and coordinates
exactly, and which serializes to JSON.
"""
from __future__ import annotations

import json

from .exact import make_point, strictly_on_segment, to_fraction
from .model import Edge, Face, GeometryError, Mesh, Vertex, _enc


# ------------------------------------------------------------ patch replay
def apply_entry(mesh: Mesh, entry: dict) -> None:
    """Replay one structured patch entry (no validation; patches are trusted)."""
    op = entry["op"]
    if op == "add_vertex":
        mesh.vertices[entry["vid"]] = Vertex(
            entry["vid"], to_fraction(entry["x"]), to_fraction(entry["y"])
        )
    elif op == "remove_vertex":
        del mesh.vertices[entry["vid"]]
    elif op == "set_vertex":
        vertex = mesh.vertices[entry["vid"]]
        vertex.x = to_fraction(entry["x"])
        vertex.y = to_fraction(entry["y"])
    elif op == "add_edge":
        mesh.edges[entry["eid"]] = Edge(entry["eid"], entry["v1"], entry["v2"])
    elif op == "remove_edge":
        del mesh.edges[entry["eid"]]
    elif op == "set_face":
        mesh.faces[entry["fid"]].vids = list(entry["vids"])
    else:
        raise ValueError(f"unknown patch op: {op!r}")


class Patch:
    """A reversible, structured, JSON-serializable diff between mesh states."""

    def __init__(self, forward, inverse, description: str = ""):
        self.forward = list(forward)
        self.inverse = list(inverse)
        self.description = description

    def apply(self, mesh: Mesh) -> None:
        for entry in self.forward:
            apply_entry(mesh, entry)

    def revert(self, mesh: Mesh) -> None:
        for entry in self.inverse:
            apply_entry(mesh, entry)

    def to_dict(self) -> dict:
        return {
            "description": self.description,
            "forward": self.forward,
            "inverse": self.inverse,
        }

    @classmethod
    def from_dict(cls, data: dict) -> "Patch":
        return cls(data["forward"], data["inverse"], data.get("description", ""))

    def to_json(self, **kwargs) -> str:
        return json.dumps(self.to_dict(), **kwargs)

    @classmethod
    def from_json(cls, text: str) -> "Patch":
        return cls.from_dict(json.loads(text))


# --------------------------------------------------------------- transaction
class Transaction:
    """Groups mesh operations atomically; failure rolls back everything."""

    def __init__(self, mesh: Mesh, description: str = ""):
        self.mesh = mesh
        self.description = description
        self._forward: list = []
        self._inverse: list = []  # application order; reversed at commit/rollback
        self._state = "active"

    # -------------------------------------------------------- journal basics
    def _guard(self) -> None:
        if self._state != "active":
            raise RuntimeError(f"transaction is {self._state}, not active")

    def _record(self, forward_entry: dict, inverse_entry: dict) -> None:
        apply_entry(self.mesh, forward_entry)
        self._forward.append(forward_entry)
        self._inverse.append(inverse_entry)

    def _add_vertex(self, vid, x, y) -> None:
        self._record(
            {"op": "add_vertex", "vid": vid, "x": _enc(x), "y": _enc(y)},
            {"op": "remove_vertex", "vid": vid},
        )

    def _remove_vertex(self, vid) -> None:
        vertex = self.mesh.vertices[vid]
        self._record(
            {"op": "remove_vertex", "vid": vid},
            {"op": "add_vertex", "vid": vid, "x": _enc(vertex.x), "y": _enc(vertex.y)},
        )

    def _set_vertex(self, vid, x, y) -> None:
        vertex = self.mesh.vertices[vid]
        self._record(
            {"op": "set_vertex", "vid": vid, "x": _enc(x), "y": _enc(y)},
            {"op": "set_vertex", "vid": vid, "x": _enc(vertex.x), "y": _enc(vertex.y)},
        )

    def _add_edge(self, eid, v1, v2) -> None:
        self._record(
            {"op": "add_edge", "eid": eid, "v1": v1, "v2": v2},
            {"op": "remove_edge", "eid": eid},
        )

    def _remove_edge(self, eid) -> None:
        edge = self.mesh.edges[eid]
        self._record(
            {"op": "remove_edge", "eid": eid},
            {"op": "add_edge", "eid": eid, "v1": edge.v1, "v2": edge.v2},
        )

    def _set_face(self, fid, vids) -> None:
        old = list(self.mesh.faces[fid].vids)
        self._record(
            {"op": "set_face", "fid": fid, "vids": list(vids)},
            {"op": "set_face", "fid": fid, "vids": old},
        )

    # ---------------------------------------------------------- public ops
    def move_vertex(self, vid: str, x, y) -> None:
        """Move a vertex; every face referencing it follows automatically.

        Rejected (and rolled back) if any incident edge collapses to a
        point, or any affected face becomes self-intersecting, degenerate,
        or changes orientation.
        """
        self._guard()
        try:
            self._move_vertex(vid, x, y)
        except Exception:
            self.rollback()
            raise

    def split_edge(self, eid: str, x, y, new_vid=None, new_eid1=None, new_eid2=None):
        """Split an edge at an exact point strictly inside it.

        All faces bounded by the edge gain the new vertex between the old
        endpoints, preserving orientation. Returns (new_vid, eid1, eid2).
        """
        self._guard()
        try:
            return self._split_edge(eid, x, y, new_vid, new_eid1, new_eid2)
        except Exception:
            self.rollback()
            raise

    def merge_edges(self, eid1: str, eid2: str, new_eid=None):
        """Merge two edges sharing a degree-2 vertex into one edge.

        The shared vertex is removed from every referencing face. If the
        merge would make any face self-intersecting, degenerate, or flip
        its orientation, the operation is rejected and rolled back.
        Returns (merged_eid, removed_vid).
        """
        self._guard()
        try:
            return self._merge_edges(eid1, eid2, new_eid)
        except Exception:
            self.rollback()
            raise

    # ------------------------------------------------------- op internals
    def _move_vertex(self, vid, x, y) -> None:
        if vid not in self.mesh.vertices:
            raise KeyError(f"unknown vertex: {vid}")
        nx, ny = make_point(x, y)
        vertex = self.mesh.vertices[vid]
        if (nx, ny) == vertex.point:
            return  # no-op: nothing journaled
        affected = self.mesh.faces_using_vertex(vid)
        old_orientation = {fid: self.mesh.face_orientation(fid) for fid in affected}
        incident = [e for e in self.mesh.edges.values() if vid in (e.v1, e.v2)]
        self._set_vertex(vid, nx, ny)
        for edge in incident:
            if self.mesh.point_of(edge.v1) == self.mesh.point_of(edge.v2):
                raise GeometryError(f"move collapses edge {edge.eid} to a point")
        for fid in affected:
            self.mesh.check_face(fid, require_orientation=old_orientation[fid])

    def _split_edge(self, eid, x, y, new_vid, new_eid1, new_eid2):
        if eid not in self.mesh.edges:
            raise KeyError(f"unknown edge: {eid}")
        edge = self.mesh.edges[eid]
        a, b = edge.v1, edge.v2
        point = make_point(x, y)
        if not strictly_on_segment(self.mesh.point_of(a), self.mesh.point_of(b), point):
            raise GeometryError(f"split point {point} is not strictly inside {eid}")
        mid = new_vid or self.mesh.fresh_id("v")
        e1 = new_eid1 or self.mesh.fresh_id("e")
        e2 = new_eid2 or self.mesh.fresh_id("e")
        for candidate, table in (
            (mid, self.mesh.vertices),
            (e1, self.mesh.edges),
            (e2, self.mesh.edges),
        ):
            if candidate in table:
                raise GeometryError(f"id already in use: {candidate}")
        affected = self.mesh.faces_using_edge(a, b)
        orientations = {fid: self.mesh.face_orientation(fid) for fid in affected}
        new_cycles = {}
        for fid in affected:
            vids = self.mesh.faces[fid].vids
            n = len(vids)
            for i in range(n):
                if {vids[i], vids[(i + 1) % n]} == {a, b}:
                    new_cycles[fid] = vids[: i + 1] + [mid] + vids[i + 1 :]
                    break
        self._remove_edge(eid)
        self._add_vertex(mid, point[0], point[1])
        self._add_edge(e1, a, mid)
        self._add_edge(e2, mid, b)
        for fid, cycle in new_cycles.items():
            self._set_face(fid, cycle)
        for fid in affected:
            self.mesh.check_face(fid, require_orientation=orientations[fid])
        return mid, e1, e2

    def _merge_edges(self, eid1, eid2, new_eid):
        if eid1 == eid2:
            raise GeometryError("cannot merge an edge with itself")
        if eid1 not in self.mesh.edges:
            raise KeyError(f"unknown edge: {eid1}")
        if eid2 not in self.mesh.edges:
            raise KeyError(f"unknown edge: {eid2}")
        edge1, edge2 = self.mesh.edges[eid1], self.mesh.edges[eid2]
        shared = {edge1.v1, edge1.v2} & {edge2.v1, edge2.v2}
        if len(shared) != 1:
            raise GeometryError(f"edges {eid1} and {eid2} share no single vertex")
        vid = shared.pop()
        if self.mesh.vertex_degree(vid) != 2:
            raise GeometryError(
                f"vertex {vid} has degree {self.mesh.vertex_degree(vid)}, not 2"
            )
        a = edge1.v1 if edge1.v2 == vid else edge1.v2
        b = edge2.v1 if edge2.v2 == vid else edge2.v2
        if a == b:
            raise GeometryError("merge would create a self-loop")
        affected = self.mesh.faces_using_vertex(vid)
        orientations = {fid: self.mesh.face_orientation(fid) for fid in affected}
        new_cycles = {}
        for fid in affected:
            vids = self.mesh.faces[fid].vids
            i = vids.index(vid)
            prev_vid = vids[i - 1]
            next_vid = vids[(i + 1) % len(vids)]
            if {prev_vid, next_vid} != {a, b}:
                raise GeometryError(
                    f"face {fid} is not bounded by both edges at vertex {vid}"
                )
            cycle = vids[:i] + vids[i + 1 :]
            if len(cycle) < 3:
                raise GeometryError(f"merge would degenerate face {fid}")
            new_cycles[fid] = cycle
        merged = new_eid or self.mesh.fresh_id("e")
        if merged in self.mesh.edges:
            raise GeometryError(f"id already in use: {merged}")
        self._remove_edge(eid1)
        self._remove_edge(eid2)
        self._remove_vertex(vid)
        self._add_edge(merged, a, b)
        for fid, cycle in new_cycles.items():
            self._set_face(fid, cycle)
        for fid in affected:
            # Rejects self-intersection introduced by the merged chord.
            self.mesh.check_face(fid, require_orientation=orientations[fid])
        return merged, vid

    # --------------------------------------------------------------- finish
    def commit(self) -> Patch:
        """Seal the transaction and return its reversible patch."""
        self._guard()
        self._state = "committed"
        return Patch(
            self._forward,
            list(reversed(self._inverse)),
            description=self.description,
        )

    def rollback(self) -> None:
        """Undo every journaled mutation, restoring exact IDs and coordinates."""
        if self._state != "active":
            return
        for entry in reversed(self._inverse):
            apply_entry(self.mesh, entry)
        self._state = "rolled back"
