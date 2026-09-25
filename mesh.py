"""Planar-face mesh with shared vertices, transactional edits and
reversible structured patches.

Model
-----
- ``Mesh.vertices``: ``{vertex_id: Point}`` -- the single source of truth for
  coordinates.  Moving a vertex therefore updates every face referencing it.
- ``Mesh.faces``: ``{face_id: [vertex_id, ...]}`` -- each face is an ordered
  vertex ring.  Operations never reorder a ring, so face orientation
  (CW / CCW, i.e. the sign of the signed area) is preserved.

Degenerate cases (defined, exact -- no epsilons)
------------------------------------------------
A face is invalid when any of the following holds, checked exactly on
Fraction coordinates:
  * fewer than 3 vertices;
  * a zero-length edge (two consecutive vertices at identical coordinates);
  * a repeated coordinate anywhere in the ring (self-touching vertex);
  * two non-adjacent edges sharing any point (crossing or touching);
  * zero signed area.
Consecutive collinear edges are legal (that is what merge_edges produces).

Patches
-------
Every mutating operation returns a forward patch (a plain dict).
``invert_patch`` produces the inverse patch; applying inverse patches in
reverse order restores vertex IDs and coordinates exactly.
"""

from fractions import Fraction

from geom import Point, orient, on_segment, segments_intersect, signed_area2

OP_MOVE = "move_vertex"
OP_SPLIT = "split_edge"
OP_MERGE = "merge_edges"


class ValidationError(Exception):
    """Raised when an operation would violate mesh validity."""


def validate_polygon(points):
    """Raise ValidationError unless ``points`` form a valid simple ring."""
    n = len(points)
    if n < 3:
        raise ValidationError("face must have at least 3 vertices")
    for i in range(n):
        if points[i] == points[(i + 1) % n]:
            raise ValidationError(f"zero-length edge at position {i}")
    if len(set(points)) != n:
        raise ValidationError("face repeats a vertex position (self-touching)")
    for i in range(n):
        a1, a2 = points[i], points[(i + 1) % n]
        for j in range(i + 1, n):
            if j == i + 1 or (i == 0 and j == n - 1):
                continue  # adjacent edges legitimately share an endpoint
            b1, b2 = points[j], points[(j + 1) % n]
            if segments_intersect(a1, a2, b1, b2):
                raise ValidationError(
                    f"self-intersection: edge {i} meets edge {j}")
    if signed_area2(points) == 0:
        raise ValidationError("degenerate zero-area face")


class Mesh:
    def __init__(self):
        self.vertices = {}
        self.faces = {}

    # ------------------------------------------------------------------ setup
    def add_vertex(self, vid, x, y):
        if vid in self.vertices:
            raise ValidationError(f"duplicate vertex id {vid!r}")
        self.vertices[vid] = Point(x, y)

    def add_face(self, fid, vids):
        if fid in self.faces:
            raise ValidationError(f"duplicate face id {fid!r}")
        missing = [v for v in vids if v not in self.vertices]
        if missing:
            raise ValidationError(f"face references unknown vertices {missing}")
        validate_polygon([self.vertices[v] for v in vids])
        self.faces[fid] = list(vids)

    # --------------------------------------------------------------- helpers
    def faces_containing(self, vid):
        return [fid for fid, ring in self.faces.items() if vid in ring]

    def face_points(self, fid):
        return [self.vertices[v] for v in self.faces[fid]]

    def orientation(self, fid):
        """+1 for CCW, -1 for CW."""
        area2 = signed_area2(self.face_points(fid))
        return 1 if area2 > 0 else -1

    def _validate_faces(self, fids):
        for fid in fids:
            validate_polygon(self.face_points(fid))

    def __eq__(self, other):
        return (isinstance(other, Mesh)
                and self.vertices == other.vertices
                and self.faces == other.faces)

    def snapshot(self):
        """Deep-enough copy for equality comparisons."""
        clone = Mesh()
        clone.vertices = dict(self.vertices)
        clone.faces = {fid: list(ring) for fid, ring in self.faces.items()}
        return clone

    # ------------------------------------------------------------ operations
    def move_vertex(self, vid, x, y):
        """Move a vertex; every face referencing it is affected at once."""
        if vid not in self.vertices:
            raise ValidationError(f"unknown vertex {vid!r}")
        old = self.vertices[vid]
        new = Point(x, y)
        affected = self.faces_containing(vid)
        self.vertices[vid] = new
        try:
            self._validate_faces(affected)
        except Exception:
            self.vertices[vid] = old
            raise
        return {"op": OP_MOVE, "vid": vid,
                "old": (old.x, old.y), "new": (new.x, new.y)}

    def split_edge(self, fid, index, new_vid, x, y):
        """Insert ``new_vid`` on the edge ring[index] -> ring[index+1].

        The new point must lie strictly on that edge (exact collinearity and
        betweenness, endpoints excluded).
        """
        if fid not in self.faces:
            raise ValidationError(f"unknown face {fid!r}")
        if new_vid in self.vertices:
            raise ValidationError(f"duplicate vertex id {new_vid!r}")
        ring = self.faces[fid]
        n = len(ring)
        if not 0 <= index < n:
            raise ValidationError(f"edge index {index} out of range")
        a = self.vertices[ring[index]]
        b = self.vertices[ring[(index + 1) % n]]
        p = Point(x, y)
        if p == a or p == b:
            raise ValidationError("split point coincides with an edge endpoint")
        if orient(a, b, p) != 0 or not on_segment(a, b, p):
            raise ValidationError("split point is not on the edge")
        self.vertices[new_vid] = p
        self.faces[fid] = ring[:index + 1] + [new_vid] + ring[index + 1:]
        try:
            self._validate_faces([fid])
        except Exception:
            self.faces[fid] = ring
            del self.vertices[new_vid]
            raise
        return {"op": OP_SPLIT, "fid": fid, "at": index + 1,
                "new_vid": new_vid, "pos": (p.x, p.y)}

    def merge_edges(self, fid, index):
        """Remove ring[index], merging its two incident edges into one.

        Rejected when the shortcut edge would make the face self-intersecting
        or otherwise degenerate.  The removed vertex stays in the vertex store
        (other faces may still reference it).
        """
        if fid not in self.faces:
            raise ValidationError(f"unknown face {fid!r}")
        ring = self.faces[fid]
        n = len(ring)
        if n < 4:
            raise ValidationError("cannot merge edges of a face with < 4 vertices")
        if not 0 <= index < n:
            raise ValidationError(f"vertex index {index} out of range")
        removed = ring[index]
        pos = self.vertices[removed]
        self.faces[fid] = ring[:index] + ring[index + 1:]
        try:
            self._validate_faces([fid])
        except Exception:
            self.faces[fid] = ring
            raise
        return {"op": OP_MERGE, "fid": fid, "at": index,
                "removed_vid": removed, "pos": (pos.x, pos.y),
                "drop_vertex": False}

    # ---------------------------------------------------------------- patches
    def apply_patch(self, patch):
        """Apply a structured patch without re-validation (trusted input)."""
        op = patch["op"]
        if op == OP_MOVE:
            vid = patch["vid"]
            if vid not in self.vertices:
                raise ValidationError(f"unknown vertex {vid!r}")
            self.vertices[vid] = Point(*patch["new"])
        elif op == OP_SPLIT:
            ring = self.faces[patch["fid"]]
            at = patch["at"]
            vid = patch["new_vid"]
            self.vertices.setdefault(vid, Point(*patch["pos"]))
            self.faces[patch["fid"]] = ring[:at] + [vid] + ring[at:]
        elif op == OP_MERGE:
            ring = self.faces[patch["fid"]]
            at = patch["at"]
            vid = patch["removed_vid"]
            if ring[at] != vid:
                raise ValidationError("patch does not match mesh state")
            self.faces[patch["fid"]] = ring[:at] + ring[at + 1:]
            if patch.get("drop_vertex"):
                self.vertices.pop(vid, None)
        else:
            raise ValidationError(f"unknown patch op {op!r}")


def invert_patch(patch):
    """Return the patch that undoes ``patch`` exactly."""
    op = patch["op"]
    if op == OP_MOVE:
        return {"op": OP_MOVE, "vid": patch["vid"],
                "old": patch["new"], "new": patch["old"]}
    if op == OP_SPLIT:
        return {"op": OP_MERGE, "fid": patch["fid"], "at": patch["at"],
                "removed_vid": patch["new_vid"], "pos": patch["pos"],
                "drop_vertex": True}
    if op == OP_MERGE:
        return {"op": OP_SPLIT, "fid": patch["fid"], "at": patch["at"],
                "new_vid": patch["removed_vid"], "pos": patch["pos"]}
    raise ValidationError(f"unknown patch op {op!r}")


# ------------------------------------------------------------ serialization
def encode_patch(patch):
    """Convert a patch to a JSON-safe dict (Fractions become [num, den])."""
    def enc(value):
        if isinstance(value, Fraction):
            return {"__fraction__": [value.numerator, value.denominator]}
        if isinstance(value, tuple):
            return [enc(v) for v in value]
        if isinstance(value, dict):
            return {k: enc(v) for k, v in value.items()}
        return value
    return enc(patch)


def decode_patch(data):
    """Inverse of encode_patch."""
    def dec(value):
        if isinstance(value, dict):
            if set(value) == {"__fraction__"}:
                num, den = value["__fraction__"]
                return Fraction(num, den)
            return {k: dec(v) for k, v in value.items()}
        if isinstance(value, list):
            out = [dec(v) for v in value]
            return tuple(out) if len(out) == 2 and all(
                isinstance(v, Fraction) for v in out) else out
        return value
    return dec(data)


class Transaction:
    """A batch of operations applied atomically to a mesh.

    On commit, operations run in order; if any of them fails, every
    completed operation is undone via its inverse patch, so the mesh is
    left exactly as it was before the transaction.
    """

    def __init__(self, mesh):
        self.mesh = mesh
        self._ops = []

    def move_vertex(self, vid, x, y):
        self._ops.append((OP_MOVE, (vid, x, y)))
        return self

    def split_edge(self, fid, index, new_vid, x, y):
        self._ops.append((OP_SPLIT, (fid, index, new_vid, x, y)))
        return self

    def merge_edges(self, fid, index):
        self._ops.append((OP_MERGE, (fid, index)))
        return self

    def commit(self):
        """Apply all ops; roll back everything on failure.

        Returns the list of forward patches on success.
        """
        done = []
        try:
            for name, args in self._ops:
                done.append(getattr(self.mesh, name)(*args))
        except Exception:
            for patch in reversed(done):
                self.mesh.apply_patch(invert_patch(patch))
            raise
        return done
