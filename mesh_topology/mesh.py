"""Half-edge mesh built from a triangle face list.

The mesh maintains, incrementally on every edit:

- ``_halfedges``: directed half-edge (u, v) -> [(face_id, slot)], i.e. the
  twin relation between opposite half-edges is derivable by key lookup;
- ``_edge_faces``: undirected edge key -> [face_id];
- lazily computed, invalidated-on-edit caches for boundary loops and
  vertex fans (vertex link connectivity).

``verify_consistency`` recomputes every structure from the raw face list
(full rebuild) and compares it against the incrementally maintained state.

Index-degenerate faces (a vertex index occurring more than once) are kept
in the face list, reported by diagnostics, but excluded from all
topological structures so they can never masquerade as adjacency.
"""
from __future__ import annotations

from .geometry import exact_point


class MeshError(Exception):
    """Raised when a topological edit or a consistency check fails."""


def edge_key(u, v):
    """Canonical (order-independent) key for an undirected edge."""
    return (u, v) if repr(u) <= repr(v) else (v, u)


def _sorted(values):
    return tuple(sorted(values, key=repr))


def _norm_map(mapping):
    return {key: _sorted(values) for key, values in mapping.items()}


class HalfEdgeMesh:
    def __init__(self, faces=(), vertices=None):
        self._vertices = {}
        self._faces = {}
        self._next_fid = 1
        self._halfedges = {}
        self._edge_faces = {}
        self._loops_cache = None
        self._fans_cache = None
        for vid, coord in (vertices or {}).items():
            self.add_vertex(vid, coord)
        for face in faces:
            self.insert_face(*face)

    # ------------------------------------------------------------------
    # read-only views
    # ------------------------------------------------------------------
    @property
    def vertices(self):
        return dict(self._vertices)

    @property
    def faces(self):
        return dict(self._faces)

    @property
    def halfedges(self):
        return {key: tuple(value) for key, value in self._halfedges.items()}

    @property
    def edge_faces(self):
        return {key: tuple(value) for key, value in self._edge_faces.items()}

    # ------------------------------------------------------------------
    # internal maintenance of derived structures
    # ------------------------------------------------------------------
    def _invalidate_caches(self):
        self._loops_cache = None
        self._fans_cache = None

    @staticmethod
    def _directed_edges(face):
        a, b, c = face
        return ((a, b), (b, c), (c, a))

    def _link(self, fid):
        for slot, (u, v) in enumerate(self._directed_edges(self._faces[fid])):
            self._halfedges.setdefault((u, v), []).append((fid, slot))
            self._edge_faces.setdefault(edge_key(u, v), []).append(fid)

    def _unlink(self, fid):
        for slot, (u, v) in enumerate(self._directed_edges(self._faces[fid])):
            entries = self._halfedges.get((u, v))
            if entries and (fid, slot) in entries:
                entries.remove((fid, slot))
                if not entries:
                    del self._halfedges[(u, v)]
            key = edge_key(u, v)
            fids = self._edge_faces.get(key)
            if fids and fid in fids:
                fids.remove(fid)
                if not fids:
                    del self._edge_faces[key]

    # ------------------------------------------------------------------
    # vertices
    # ------------------------------------------------------------------
    def add_vertex(self, vid, coord=None):
        if vid in self._vertices:
            raise MeshError(f"vertex {vid!r} already exists")
        self._vertices[vid] = exact_point(coord) if coord is not None else None
        self._invalidate_caches()
        return vid

    def _fresh_vid(self):
        if all(isinstance(v, int) and not isinstance(v, bool)
               for v in self._vertices):
            candidate = max(self._vertices, default=-1) + 1
            while candidate in self._vertices:
                candidate += 1
            return candidate
        index = 0
        while f"w{index}" in self._vertices:
            index += 1
        return f"w{index}"

    def _vertex_is_used(self, vid):
        return any(vid in face for face in self._faces.values())

    # ------------------------------------------------------------------
    # transactional edit operations
    # ------------------------------------------------------------------
    def insert_face(self, a, b, c, fid=None):
        if fid is None:
            fid = self._next_fid
            while fid in self._faces:
                fid += 1
            self._next_fid = fid + 1
        elif fid in self._faces:
            raise MeshError(f"face id {fid!r} already exists")
        face = (a, b, c)
        self._faces[fid] = face
        for vid in face:
            self._vertices.setdefault(vid, None)
        if len(set(face)) == 3:
            self._link(fid)
        self._invalidate_caches()
        return fid

    def delete_face(self, fid):
        if fid not in self._faces:
            raise MeshError(f"face {fid!r} does not exist")
        if len(set(self._faces[fid])) == 3:
            self._unlink(fid)
        del self._faces[fid]
        self._invalidate_caches()

    def flip_face(self, fid):
        if fid not in self._faces:
            raise MeshError(f"face {fid!r} does not exist")
        face = self._faces[fid]
        linked = len(set(face)) == 3
        if linked:
            self._unlink(fid)
        self._faces[fid] = (face[0], face[2], face[1])
        if linked:
            self._link(fid)
        self._invalidate_caches()

    def split_edge(self, u, v, new_vid=None, coord=None):
        """Split undirected edge {u, v}; every incident face becomes two."""
        key = edge_key(u, v)
        fids = self._edge_faces.get(key)
        if not fids:
            raise MeshError(f"edge {key!r} does not exist")
        if new_vid is None:
            new_vid = self._fresh_vid()
        elif new_vid in self._vertices and self._vertex_is_used(new_vid):
            raise MeshError(f"vertex {new_vid!r} is already in use")
        if new_vid not in self._vertices:
            if coord is None:
                cu, cv = self._vertices.get(u), self._vertices.get(v)
                if cu is not None and cv is not None:
                    coord = tuple((x + y) / 2 for x, y in zip(cu, cv))
            self.add_vertex(new_vid, coord)
        for fid in list(fids):
            face = self._faces[fid]
            for slot in range(3):
                x, y = face[slot], face[(slot + 1) % 3]
                z = face[(slot + 2) % 3]
                if edge_key(x, y) == key:
                    self.delete_face(fid)
                    self.insert_face(x, new_vid, z)
                    self.insert_face(new_vid, y, z)
                    break
        return new_vid

    def split_vertex(self, vid, fids, new_vid):
        """Move ``fids`` from ``vid`` to a fresh vertex (bowtie repair)."""
        if vid not in self._vertices:
            raise MeshError(f"vertex {vid!r} does not exist")
        if new_vid in self._vertices and self._vertex_is_used(new_vid):
            raise MeshError(f"vertex {new_vid!r} is already in use")
        for fid in fids:
            if fid not in self._faces or vid not in self._faces[fid]:
                raise MeshError(
                    f"face {fid!r} is not incident to vertex {vid!r}")
        if new_vid not in self._vertices:
            self.add_vertex(new_vid, self._vertices[vid])
        for fid in fids:
            face = self._faces[fid]
            linked = len(set(face)) == 3
            if linked:
                self._unlink(fid)
            new_face = tuple(new_vid if v == vid else v for v in face)
            self._faces[fid] = new_face
            if len(set(new_face)) == 3:
                self._link(fid)
        self._invalidate_caches()
        return new_vid

    # ------------------------------------------------------------------
    # boundary loops and vertex fans
    # ------------------------------------------------------------------
    @staticmethod
    def _compute_boundary_loops(halfedges):
        boundary = [he for he in halfedges
                    if (he[1], he[0]) not in halfedges]
        outgoing = {}
        for he in boundary:
            outgoing.setdefault(he[0], []).append(he)
        for edges in outgoing.values():
            edges.sort(key=repr)
        used = set()
        chains = []
        for start in sorted(boundary, key=repr):
            if start in used:
                continue
            chain = [start[0]]
            current = start
            while current not in used:
                used.add(current)
                chain.append(current[1])
                candidates = [he for he in outgoing.get(current[1], [])
                              if he not in used]
                if not candidates:
                    break
                current = candidates[0]
            chains.append(tuple(chain))
        return tuple(chains)

    def _compute_vertex_fans(self, edge_faces):
        incident = {}
        for fid, face in self._faces.items():
            if len(set(face)) != 3:
                continue
            for vid in face:
                incident.setdefault(vid, set()).add(fid)
        fans = {}
        for vid, fids in incident.items():
            parent = {fid: fid for fid in fids}

            def find(x):
                while parent[x] != x:
                    parent[x] = parent[parent[x]]
                    x = parent[x]
                return x

            for key, key_fids in edge_faces.items():
                if vid not in key:
                    continue
                group = [f for f in key_fids if f in parent]
                for other in group[1:]:
                    ra, rb = find(group[0]), find(other)
                    if ra != rb:
                        parent[ra] = rb
            components = {}
            for fid in fids:
                components.setdefault(find(fid), set()).add(fid)
            fans[vid] = _sorted(
                frozenset(component) for component in components.values())
        return fans

    def boundary_loops(self):
        if self._loops_cache is None:
            self._loops_cache = self._compute_boundary_loops(self._halfedges)
        return self._loops_cache

    def vertex_fans(self):
        if self._fans_cache is None:
            self._fans_cache = self._compute_vertex_fans(self._edge_faces)
        return self._fans_cache

    # ------------------------------------------------------------------
    # snapshots, rebuild, consistency
    # ------------------------------------------------------------------
    def snapshot(self):
        return {
            "faces": dict(self._faces),
            "vertices": dict(self._vertices),
            "next_fid": self._next_fid,
        }

    def restore(self, snap):
        self._faces = dict(snap["faces"])
        self._vertices = dict(snap["vertices"])
        self._next_fid = snap["next_fid"]
        self.rebuild()

    def rebuild(self):
        """Full rebuild of every derived structure from the face list."""
        self._halfedges = {}
        self._edge_faces = {}
        for fid, face in self._faces.items():
            if len(set(face)) == 3:
                self._link(fid)
        self._invalidate_caches()

    def verify_consistency(self):
        """Compare incrementally maintained state against a full rebuild."""
        halfedges, edge_faces = {}, {}
        for fid, face in self._faces.items():
            if len(set(face)) != 3:
                continue
            for slot, (u, v) in enumerate(self._directed_edges(face)):
                halfedges.setdefault((u, v), []).append((fid, slot))
                edge_faces.setdefault(edge_key(u, v), []).append(fid)
        if _norm_map(halfedges) != _norm_map(self._halfedges):
            raise MeshError("half-edge map out of sync with face list")
        if _norm_map(edge_faces) != _norm_map(self._edge_faces):
            raise MeshError("edge-face map out of sync with face list")
        if self.boundary_loops() != self._compute_boundary_loops(halfedges):
            raise MeshError("boundary loop cache out of sync")
        if self.vertex_fans() != self._compute_vertex_fans(edge_faces):
            raise MeshError("vertex fan cache out of sync")
        return True

    def canonical(self):
        """Order-independent canonical form, used to compare snapshots."""
        fans = {vid: tuple(sorted((tuple(_sorted(c)) for c in comps),
                                  key=repr))
                for vid, comps in self.vertex_fans().items()}
        return (
            _sorted(self._faces.items()),
            _sorted(self._vertices.items()),
            _sorted((k, _sorted(v)) for k, v in self._halfedges.items()),
            _sorted((k, _sorted(v)) for k, v in self._edge_faces.items()),
            self.boundary_loops(),
            _sorted(fans.items()),
        )
