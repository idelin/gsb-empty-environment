"""Demo: transactional editing of a mesh with shared vertices.

Run with:  python3.11 main.py
"""
from fractions import Fraction

from geomesh import GeometryError, Mesh, Patch, Transaction


def build_two_squares() -> Mesh:
    """Two unit-square faces sharing the edge B-C."""
    mesh = Mesh()
    for vid, x, y in [
        ("A", 0, 0), ("B", 4, 0), ("C", 4, 4),
        ("D", 0, 4), ("G", 8, 0), ("H", 8, 4),
    ]:
        mesh.add_vertex(vid, x, y)
    for eid, v1, v2 in [
        ("eAB", "A", "B"), ("eBC", "B", "C"), ("eCD", "C", "D"),
        ("eDA", "D", "A"), ("eBG", "B", "G"), ("eGH", "G", "H"),
        ("eHC", "H", "C"),
    ]:
        mesh.add_edge(eid, v1, v2)
    mesh.add_face("left", ["A", "B", "C", "D"])
    mesh.add_face("right", ["B", "G", "H", "C"])
    return mesh


def show(mesh, title):
    print(f"--- {title} ---")
    for fid, face in sorted(mesh.faces.items()):
        coords = ", ".join(f"{vid}{mesh.point_of(vid)}" for vid in face.vids)
        print(f"  face {fid}: {coords}")
    print(f"  edges: {sorted(mesh.edges)}")


def main():
    mesh = build_two_squares()
    original = mesh.to_dict()
    show(mesh, "initial mesh (B and C are shared by both faces)")

    # 1. One transaction: move a shared vertex, split an edge, merge it back.
    tx = Transaction(mesh, description="move B, split eCD, merge it back")
    tx.move_vertex("B", Fraction(3), Fraction(1))
    print("\n[1] moved shared vertex B -> (3, 1); both faces follow:")
    show(mesh, "after move_vertex")

    mid, e1, e2 = tx.split_edge("eCD", Fraction(2), 4)
    print(f"\n[2] split eCD at (2, 4): new vertex {mid}, edges {e1}, {e2}")
    show(mesh, "after split_edge")

    merged, removed = tx.merge_edges(e1, e2)
    print(f"\n[3] merged {e1}+{e2} back: removed vertex {removed}, edge {merged}")
    show(mesh, "after merge_edges")

    patch = tx.commit()
    print("\n[4] committed patch (JSON, exact rational encoding):")
    print(patch.to_json(indent=2))

    # 2. Reversible patch: revert restores every ID and coordinate.
    edited = mesh.to_dict()
    patch.revert(mesh)
    assert mesh.to_dict() == original, "revert must restore the original state"
    print("\n[5] patch.revert()  -> mesh identical to the original: OK")
    patch.apply(mesh)
    assert mesh.to_dict() == edited, "apply must reproduce the edited state"
    print("[6] patch.apply()   -> mesh identical to the edited state: OK")

    # 3. JSON round-trip of the patch itself.
    clone = Patch.from_json(patch.to_json())
    clone.revert(mesh)
    assert mesh.to_dict() == original
    print("[7] JSON round-tripped patch reverts identically: OK")

    # 4. Atomic rollback: a failing op undoes the whole transaction.
    tx2 = Transaction(mesh, description="doomed transaction")
    tx2.move_vertex("A", 1, 1)
    tx2.split_edge("eBC", 4, 2)
    try:
        tx2.move_vertex("A", 4, 0)  # collapses edge eAB onto B -> rejected
    except GeometryError as exc:
        print(f"\n[8] failing op rejected: {exc}")
    assert mesh.to_dict() == original, "rollback must undo the whole transaction"
    print("    whole transaction rolled back (move + split undone): OK")

    # 5. Merge that would self-intersect is rejected.
    mesh2 = Mesh()
    for vid, x, y in [
        ("P", 0, 2), ("V", 2, 0), ("Q", 4, 2), ("R", 1, 3), ("S", 2, 1),
    ]:
        mesh2.add_vertex(vid, x, y)
    for eid, v1, v2 in [
        ("ePV", "P", "V"), ("eVQ", "V", "Q"), ("eQR", "Q", "R"),
        ("eRS", "R", "S"), ("eSP", "S", "P"),
    ]:
        mesh2.add_edge(eid, v1, v2)
    mesh2.add_face("f", ["P", "V", "Q", "R", "S"])
    before = mesh2.to_dict()
    tx3 = Transaction(mesh2, description="self-intersecting merge")
    try:
        tx3.merge_edges("ePV", "eVQ")  # chord P-Q would cross edge R-S
    except GeometryError as exc:
        print(f"\n[9] self-intersecting merge rejected: {exc}")
    assert mesh2.to_dict() == before
    print("    mesh unchanged after rejection: OK")

    print("\nAll demo checks passed.")


if __name__ == "__main__":
    main()
