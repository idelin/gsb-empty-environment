"""Topological and geometric diagnostics for a HalfEdgeMesh.

Definitions (all exact, no epsilon anywhere):

- boundary edge: undirected edge incident to exactly one non-degenerate
  face;
- non-manifold edge: undirected edge incident to three or more
  non-degenerate faces;
- orientation conflict: edge shared by exactly two non-degenerate,
  non-duplicate faces that traverse it in the same direction;
- duplicate faces: non-degenerate faces with the same vertex set; a
  duplicate pair sharing an edge is reported as duplicate adjacency and
  never counts as normal two-manifold adjacency;
- degenerate face (index): a vertex index occurs more than once;
- zero-area face: distinct indices but the exact cross product vanishes
  (requires coordinates for all three vertices);
- isolated vertex: vertex not incident to any non-degenerate face;
- bowtie (non-manifold) vertex: vertex whose incident-face fan has more
  than one connected component -- this is exactly what the "at most two
  faces per edge" check cannot see.
"""
from __future__ import annotations

from dataclasses import dataclass

from .geometry import is_zero_area


def _sorted(values):
    return tuple(sorted(values, key=repr))


@dataclass(frozen=True)
class Diagnostics:
    boundary_edges: tuple
    nonmanifold_edges: tuple
    orientation_conflicts: tuple
    duplicate_face_groups: tuple
    duplicate_adjacency_edges: tuple
    degenerate_faces: tuple
    zero_area_faces: tuple
    isolated_vertices: tuple
    bowtie_vertices: tuple

    def is_clean(self, allow_boundary=True):
        problems = (
            self.nonmanifold_edges,
            self.orientation_conflicts,
            self.duplicate_face_groups,
            self.degenerate_faces,
            self.zero_area_faces,
            self.bowtie_vertices,
        )
        if not allow_boundary:
            problems += (self.boundary_edges,)
        return not any(problems)


def _face_direction_on_edge(face, key):
    for slot in range(3):
        u, v = face[slot], face[(slot + 1) % 3]
        if {u, v} == set(key):
            return (u, v)
    return None


def diagnose(mesh):
    faces = mesh.faces
    edge_faces = mesh.edge_faces
    vertices = mesh.vertices

    degenerate = _sorted(
        (fid, face) for fid, face in faces.items() if len(set(face)) < 3)

    by_triple = {}
    for fid, face in faces.items():
        if len(set(face)) == 3:
            by_triple.setdefault(_sorted(face), []).append(fid)
    duplicate_groups = _sorted(
        _sorted(group) for group in by_triple.values() if len(group) > 1)

    boundary, nonmanifold, conflicts, duplicate_adjacency = [], [], [], []
    for key in sorted(edge_faces, key=repr):
        fids = edge_faces[key]
        if len(fids) == 1:
            boundary.append(key)
        elif len(fids) == 2:
            triples = {_sorted(faces[f]) for f in fids}
            if len(triples) == 1:
                duplicate_adjacency.append(key)
            else:
                dirs = [_face_direction_on_edge(faces[f], key) for f in fids]
                if dirs[0] == dirs[1]:
                    conflicts.append((key, _sorted(fids)))
        else:
            nonmanifold.append((key, len(fids), _sorted(fids)))

    zero_area = []
    for fid, face in faces.items():
        if len(set(face)) != 3:
            continue
        coords = [vertices.get(v) for v in face]
        if all(c is not None for c in coords) and is_zero_area(*coords):
            zero_area.append(fid)

    used = set()
    for face in faces.values():
        if len(set(face)) == 3:
            used.update(face)
    isolated = _sorted(v for v in vertices if v not in used)

    bowtie = _sorted(
        (vid, len(components))
        for vid, components in mesh.vertex_fans().items()
        if len(components) > 1)

    return Diagnostics(
        boundary_edges=_sorted(boundary),
        nonmanifold_edges=_sorted(nonmanifold),
        orientation_conflicts=_sorted(conflicts),
        duplicate_face_groups=duplicate_groups,
        duplicate_adjacency_edges=_sorted(duplicate_adjacency),
        degenerate_faces=degenerate,
        zero_area_faces=_sorted(zero_area),
        isolated_vertices=isolated,
        bowtie_vertices=bowtie,
    )


def format_diagnostics(diag):
    lines = []

    def section(title, items, fmt=repr):
        lines.append(f"{title}: {len(items)}")
        for item in items:
            lines.append(f"  - {fmt(item)}")

    section("boundary edges", diag.boundary_edges)
    section("non-manifold edges", diag.nonmanifold_edges)
    section("orientation conflicts", diag.orientation_conflicts)
    section("duplicate face groups", diag.duplicate_face_groups)
    section("duplicate adjacency edges", diag.duplicate_adjacency_edges)
    section("degenerate faces (repeated index)", diag.degenerate_faces)
    section("zero-area faces (exact)", diag.zero_area_faces)
    section("isolated vertices", diag.isolated_vertices)
    section("bowtie vertices", diag.bowtie_vertices)
    return "\n".join(lines)
