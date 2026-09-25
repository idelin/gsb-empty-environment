"""Local repair suggestions.

Every suggestion is an independent, executable :class:`Patch`: a list of
preconditions plus a list of operations. Patches are never applied
automatically. ``Patch.apply`` runs inside a transaction: preconditions
are re-checked against the live mesh, and any failure (unmet
precondition or failing operation) rolls the mesh back to its original
topology.
"""
from __future__ import annotations

from dataclasses import dataclass

from .diagnostics import diagnose
from .mesh import MeshError
from .transactions import Transaction


class PatchError(MeshError):
    """Raised when a patch cannot be applied (unmet precondition)."""


def _sorted(values):
    return tuple(sorted(values, key=repr))


@dataclass(frozen=True)
class Precondition:
    description: str
    check: object


@dataclass(frozen=True)
class Op:
    kind: str
    args: tuple


@dataclass(frozen=True)
class Patch:
    pid: str
    description: str
    preconditions: tuple
    ops: tuple

    def unmet_preconditions(self, mesh):
        return [p.description for p in self.preconditions
                if not p.check(mesh)]

    def apply(self, mesh):
        with Transaction(mesh):
            unmet = self.unmet_preconditions(mesh)
            if unmet:
                raise PatchError(
                    f"patch {self.pid} not applicable: "
                    + "; ".join(unmet))
            for op in self.ops:
                getattr(mesh, op.kind)(*op.args)
        return mesh

    def describe(self):
        lines = [f"[{self.pid}] {self.description}"]
        lines.append("  preconditions:")
        for pre in self.preconditions:
            lines.append(f"    - {pre.description}")
        lines.append("  operations:")
        for op in self.ops:
            lines.append(f"    - {op.kind}{op.args!r}")
        return "\n".join(lines)


def _same_direction(mesh, key):
    from .diagnostics import _face_direction_on_edge
    fids = mesh.edge_faces.get(key, ())
    if len(fids) != 2:
        return False
    faces = mesh.faces
    return (_face_direction_on_edge(faces[fids[0]], key)
            == _face_direction_on_edge(faces[fids[1]], key))


def suggest_repairs(mesh):
    diag = diagnose(mesh)
    patches = []
    counter = 0

    def next_pid():
        nonlocal counter
        counter += 1
        return f"R{counter}"

    for key, fids in diag.orientation_conflicts:
        for flip in fids:
            keep = fids[1] if flip == fids[0] else fids[0]
            patches.append(Patch(
                next_pid(),
                f"flip face {flip} to fix orientation conflict "
                f"on edge {key}",
                preconditions=(
                    Precondition(
                        f"faces {keep} and {flip} exist",
                        lambda m, a=keep, b=flip:
                            a in m.faces and b in m.faces),
                    Precondition(
                        f"edge {key} is shared by exactly faces {fids}",
                        lambda m, k=key, fs=fids:
                            set(m.edge_faces.get(k, ())) == set(fs)),
                    Precondition(
                        f"both faces still traverse edge {key} same way",
                        lambda m, k=key: _same_direction(m, k)),
                ),
                ops=(Op("flip_face", (flip,)),),
            ))

    for group in diag.duplicate_face_groups:
        keep, drop = group[0], group[1:]
        patches.append(Patch(
            next_pid(),
            f"delete {len(drop)} duplicate copy/copies of face {keep}",
            preconditions=(
                Precondition(
                    f"faces {group} all still exist",
                    lambda m, g=group: all(f in m.faces for f in g)),
                Precondition(
                    f"faces {group} still share one vertex set",
                    lambda m, g=group: len(
                        {_sorted(m.faces[f]) for f in g
                         if f in m.faces}) == 1),
            ),
            ops=tuple(Op("delete_face", (f,)) for f in drop),
        ))

    for key, count, fids in diag.nonmanifold_edges:
        if count == 3:
            for drop in fids:
                patches.append(Patch(
                    next_pid(),
                    f"delete face {drop} so edge {key} keeps 2 faces",
                    preconditions=(
                        Precondition(
                            f"edge {key} currently has faces {fids}",
                            lambda m, k=key, fs=fids:
                                set(m.edge_faces.get(k, ())) == set(fs)),
                    ),
                    ops=(Op("delete_face", (drop,)),),
                ))
        else:
            drop = fids[2:]
            patches.append(Patch(
                next_pid(),
                f"delete faces {drop} so edge {key} keeps 2 faces",
                preconditions=(
                    Precondition(
                        f"edge {key} currently has faces {fids}",
                        lambda m, k=key, fs=fids:
                            set(m.edge_faces.get(k, ())) == set(fs)),
                ),
                ops=tuple(Op("delete_face", (f,)) for f in drop),
            ))

    for vid, _components in diag.bowtie_vertices:
        fans = mesh.vertex_fans()[vid]
        for index, component in enumerate(fans[1:], start=1):
            new_vid = ("split", vid, index)
            patches.append(Patch(
                next_pid(),
                f"split bowtie vertex {vid}: move {len(component)} "
                f"face(s) to new vertex {new_vid!r}",
                preconditions=(
                    Precondition(
                        f"vertex {vid} is still a bowtie vertex",
                        lambda m, v=vid: len(
                            m.vertex_fans().get(v, ())) > 1),
                    Precondition(
                        f"vertex id {new_vid!r} is free",
                        lambda m, w=new_vid: w not in m.vertices),
                ),
                ops=(Op("split_vertex",
                        (vid, _sorted(component), new_vid)),),
            ))

    return patches
