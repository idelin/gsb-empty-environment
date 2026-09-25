"""Mesh data model: shared vertices, edges and oriented faces.

A mesh owns three entity tables keyed by stable string IDs:
  * vertices: exact coordinates (Fraction), shared by any number of faces
  * edges:    vertex pairs (standalone polylines and/or face boundaries)
  * faces:    oriented vertex cycles (positive signed area = CCW)

Because faces reference vertices by ID, moving a shared vertex through a
transaction automatically updates every referencing face.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Dict, List

from .exact import polygon_is_simple, signed_area2, to_fraction


class GeometryError(Exception):
    """Raised when an operation would violate mesh topology or geometry."""


@dataclass
class Vertex:
    vid: str
    x: Fraction
    y: Fraction

    @property
    def point(self):
        return (self.x, self.y)


@dataclass
class Edge:
    eid: str
    v1: str
    v2: str


@dataclass
class Face:
    fid: str
    vids: List[str]  # oriented vertex cycle, no repeated vertices


def _enc(value: Fraction):
    """JSON-friendly exact encoding: int if integral, else "p/q"."""
    if value.denominator == 1:
        return value.numerator
    return f"{value.numerator}/{value.denominator}"


class Mesh:
    def __init__(self):
        self.vertices: Dict[str, Vertex] = {}
        self.edges: Dict[str, Edge] = {}
        self.faces: Dict[str, Face] = {}
        self._counter = 0

    # ------------------------------------------------------------------ ids
    def fresh_id(self, prefix: str) -> str:
        while True:
            self._counter += 1
            candidate = f"{prefix}{self._counter}"
            if (
                candidate not in self.vertices
                and candidate not in self.edges
                and candidate not in self.faces
            ):
                return candidate

    # ---------------------------------------------------------- construction
    def add_vertex(self, vid: str, x, y) -> Vertex:
        if vid in self.vertices:
            raise GeometryError(f"duplicate vertex id: {vid}")
        vertex = Vertex(vid, to_fraction(x), to_fraction(y))
        self.vertices[vid] = vertex
        return vertex

    def add_edge(self, eid: str, v1: str, v2: str) -> Edge:
        if eid in self.edges:
            raise GeometryError(f"duplicate edge id: {eid}")
        for vid in (v1, v2):
            if vid not in self.vertices:
                raise GeometryError(f"edge {eid} references unknown vertex {vid}")
        if v1 == v2:
            raise GeometryError(f"edge {eid} is a self-loop")
        edge = Edge(eid, v1, v2)
        self.edges[eid] = edge
        return edge

    def add_face(self, fid: str, vids) -> Face:
        if fid in self.faces:
            raise GeometryError(f"duplicate face id: {fid}")
        vids = list(vids)
        for vid in vids:
            if vid not in self.vertices:
                raise GeometryError(f"face {fid} references unknown vertex {vid}")
        face = Face(fid, vids)
        self.faces[fid] = face
        try:
            self.check_face(fid)
        except Exception:
            del self.faces[fid]
            raise
        return face

    # --------------------------------------------------------------- queries
    def point_of(self, vid: str):
        return self.vertices[vid].point

    def face_coords(self, fid: str):
        return [self.point_of(vid) for vid in self.faces[fid].vids]

    def face_area2(self, fid: str) -> Fraction:
        return signed_area2(self.face_coords(fid))

    def face_orientation(self, fid: str) -> int:
        area = self.face_area2(fid)
        return (area > 0) - (area < 0)

    def vertex_degree(self, vid: str) -> int:
        return sum(1 for e in self.edges.values() if vid in (e.v1, e.v2))

    def faces_using_vertex(self, vid: str) -> List[str]:
        return [fid for fid, f in self.faces.items() if vid in f.vids]

    def faces_using_edge(self, a: str, b: str) -> List[str]:
        """Faces in which a and b are cyclically consecutive (either order)."""
        found = []
        for fid, face in self.faces.items():
            n = len(face.vids)
            for i in range(n):
                if {face.vids[i], face.vids[(i + 1) % n]} == {a, b}:
                    found.append(fid)
                    break
        return found

    # ------------------------------------------------------------ validation
    def check_face(self, fid: str, require_orientation: int = None) -> None:
        """Raise GeometryError unless the face is simple and keeps orientation."""
        coords = self.face_coords(fid)
        if not polygon_is_simple(coords):
            raise GeometryError(
                f"face {fid} is not simple (self-intersecting or degenerate)"
            )
        area = signed_area2(coords)
        if area == 0:
            raise GeometryError(f"face {fid} has zero area")
        if require_orientation is not None:
            sign = (area > 0) - (area < 0)
            if sign != require_orientation:
                raise GeometryError(f"face {fid} changed orientation")

    def validate(self) -> None:
        for edge in self.edges.values():
            for vid in (edge.v1, edge.v2):
                if vid not in self.vertices:
                    raise GeometryError(f"edge {edge.eid} dangles to {vid}")
            if self.point_of(edge.v1) == self.point_of(edge.v2):
                raise GeometryError(f"edge {edge.eid} has zero length")
        for fid in self.faces:
            self.check_face(fid)

    # ------------------------------------------------------------ snapshots
    def to_dict(self) -> dict:
        return {
            "vertices": {
                vid: [_enc(v.x), _enc(v.y)]
                for vid, v in sorted(self.vertices.items())
            },
            "edges": {eid: [e.v1, e.v2] for eid, e in sorted(self.edges.items())},
            "faces": {fid: list(f.vids) for fid, f in sorted(self.faces.items())},
        }

    def __eq__(self, other):
        if not isinstance(other, Mesh):
            return NotImplemented
        return self.to_dict() == other.to_dict()
