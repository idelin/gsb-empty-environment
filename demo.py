"""Runnable sample: shared-vertex mesh edits, transactions, rollback,
and reversible structured patches.  Run: python3 demo.py"""

from fractions import Fraction

from mesh import Mesh, Transaction, ValidationError, invert_patch


def show(mesh, title):
    print(f"--- {title}")
    for fid, ring in mesh.faces.items():
        coords = [f"{vid}={tuple(mesh.vertices[vid])}" for vid in ring]
        print(f"    {fid}: " + ", ".join(coords))


def main():
    # Two triangles sharing edge v2-v3: v2 and v3 are shared vertices.
    mesh = Mesh()
    for vid, xy in {"v1": (0, 0), "v2": (4, 0), "v3": (2, 3), "v4": (6, 0)}.items():
        mesh.add_vertex(vid, *xy)
    mesh.add_face("f1", ["v1", "v2", "v3"])
    mesh.add_face("f2", ["v2", "v4", "v3"])
    show(mesh, "initial mesh (f1, f2 share v2 and v3)")

    # 1. Move a shared vertex: both faces are affected at once.
    tx = Transaction(mesh)
    tx.move_vertex("v3", Fraction(7, 2), 5)
    patches = tx.commit()
    show(mesh, "after move_vertex(v3 -> (7/2, 5)): both faces updated")
    print(f"    forward patch: {patches[0]}")

    # 2. Split an edge at an exact rational point, then merge it back.
    tx = Transaction(mesh)
    tx.split_edge("f1", 0, "v5", 2, 0)   # midpoint of v1->v2
    tx.split_edge("f2", 1, "v6", Fraction(19, 4), Fraction(5, 2))
    patches = tx.commit()
    show(mesh, "after split_edge x2 (v5 on f1, v6 on f2)")
    tx = Transaction(mesh)
    tx.merge_edges("f1", 1)              # remove v5 again
    tx.commit()
    show(mesh, "after merge_edges(f1, v5): ring restored, v5 kept in store")
    print(f"    v5 still in vertex store: {'v5' in mesh.vertices}")

    # 3. Reversibility: undo the split of v6 via the inverse patch.
    mesh.apply_patch(invert_patch(patches[1]))
    show(mesh, "after applying inverse of the v6 split patch")

    # 4. A failing transaction rolls back completely.
    before = mesh.snapshot()
    tx = Transaction(mesh)
    tx.move_vertex("v2", 1, 1)           # would succeed
    tx.merge_edges("f2", 0)              # fails: f2 is a triangle
    try:
        tx.commit()
    except ValidationError as exc:
        print(f"--- transaction rejected: {exc}")
    print(f"    mesh unchanged after rollback: {mesh == before}")
    print(f"    v2 still at {tuple(mesh.vertices['v2'])}")

    # 5. Merge that would self-intersect is rejected.
    arrow = Mesh()
    for vid, xy in {"a": (0, 0), "b": (4, 0), "c": (4, 3),
                    "d": (2, 1), "e": (0, 3)}.items():
        arrow.add_vertex(vid, *xy)
    arrow.add_face("arrow", ["a", "b", "c", "d", "e"])
    try:
        arrow.merge_edges("arrow", 1)    # shortcut a->c would cross edge d->e
    except ValidationError as exc:
        print(f"--- self-intersecting merge rejected: {exc}")

    # 6. Exactness: a point 10^-12 off an edge is NOT on the edge.
    try:
        mesh.split_edge("f1", 0, "vx", 2, Fraction(1, 10 ** 12))
    except ValidationError as exc:
        print(f"--- near-miss split rejected exactly (no epsilon): {exc}")


if __name__ == "__main__":
    main()
