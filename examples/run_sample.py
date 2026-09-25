"""End-to-end demo: build, diagnose, suggest repairs, transactional edits.

Run from the repository root:
    python3.11 examples/run_sample.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from mesh_topology import (  # noqa: E402
    HalfEdgeMesh,
    MeshError,
    Transaction,
    diagnose,
    format_diagnostics,
    random_edits,
    suggest_repairs,
)


def main():
    vertices = {
        0: (0, 0, 0), 1: (1, 0, 0), 2: (0, 1, 0), 3: (0, 0, 1),
        4: (1, 1, 1),
        5: (2, 0, 0), 6: (2, 1, 0), 7: (2, 0, 1),
        8: (3, 0, 0), 9: (3, 1, 0),
        10: (4, 0, 0), 11: (5, 0, 0), 12: (4, 1, 0),
        13: (6, 0, 0), 14: (7, 0, 0), 15: (8, 0, 0),
        99: (9, 9, 9),
    }
    faces = [
        (0, 1, 2),           # 1: flipped on purpose (should be (0, 2, 1))
        (0, 1, 3),           # 2
        (0, 3, 2),           # 3
        (1, 2, 3),           # 4
        (0, 1, 4),           # 5: third face on edge {0, 1} -> non-manifold
        (0, 6, 5),           # 6:  second tetrahedron sharing only
        (0, 5, 7),           # 7:  vertex 0 -> bowtie vertex
        (0, 7, 6),           # 8
        (5, 6, 7),           # 9
        (8, 8, 9),           # 10: index-degenerate
        (10, 11, 12),        # 11
        (10, 11, 12),        # 12: duplicate of face 11
        (13, 14, 15),        # 13: collinear -> exact zero area
    ]
    mesh = HalfEdgeMesh(faces, vertices=vertices)

    print("=== diagnostics ===")
    diag = diagnose(mesh)
    print(format_diagnostics(diag))

    print("\n=== boundary loops ===")
    for loop in mesh.boundary_loops():
        print("  ", loop)

    print("\n=== repair suggestions (not applied automatically) ===")
    patches = suggest_repairs(mesh)
    for patch in patches:
        print(patch.describe())

    print("\n=== apply two patches explicitly ===")
    flip = next(p for p in patches if "flip face 1" in p.description)
    dup = next(p for p in patches if "duplicate" in p.description)
    flip.apply(mesh)
    dup.apply(mesh)
    print(f"applied {flip.pid}: {flip.description}")
    print(f"applied {dup.pid}: {dup.description}")
    diag = diagnose(mesh)
    print("orientation conflicts now:", len(diag.orientation_conflicts))
    print("duplicate groups now:", len(diag.duplicate_face_groups))

    print("\n=== transactional edit with rollback ===")
    before = mesh.canonical()
    try:
        with Transaction(mesh):
            mesh.insert_face(0, 1, 5)
            mesh.split_edge(0, 1)
            mesh.delete_face(12345)  # does not exist -> failure
    except MeshError as exc:
        print(f"transaction failed as expected: {exc}")
    print("topology preserved after failure:", mesh.canonical() == before)

    print("\n=== random edits vs full rebuild ===")
    log = random_edits(mesh, steps=300, seed=20260925)
    mesh.verify_consistency()
    print(f"{len(log)} random edits, incremental state matches "
          f"full rebuild: OK")


if __name__ == "__main__":
    main()
