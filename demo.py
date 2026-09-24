"""Runnable walkthrough of the hyperproj projection engine.

Covers the required scenarios:
  * A, B grouped in G; A->B must NOT count as an outgoing edge of G;
  * A->C and B->C merge into one super-edge that still lists both originals;
  * expand-then-collapse conserves statistics;
  * half-open time intervals, isolated vertices, multi-edges;
  * atomic moves, undo, named snapshots, independent forked views;
  * batch rejection on hierarchy cycles / id conflicts;
  * a final cross-check of the incremental index against a brute-force
    re-projection of every super-edge's source-edge multiset.
"""

from hyperproj import (
    AddEdge,
    AddGroup,
    AddVertex,
    Collapse,
    Expand,
    HierarchyCycleError,
    IdConflictError,
    MoveGroup,
    MoveVertex,
    Projection,
)
from hyperproj.brute import brute_projection


def show(proj, at=None):
    print(f"  supernodes : {sorted(proj.supernodes())}")
    for (u, v), ids in sorted(proj.superedges(at).items()):
        print(f"  superedge  : {u} -> {v}  x{len(ids)}  evidence={ids}")
    for node, ids in sorted(proj.internal_map(at).items()):
        print(f"  internal   : ({node}) {ids}")
    for g in sorted(proj.groups):
        print(f"  group {g:6s}: nested member count = {proj.member_count(g)}")


def main():
    proj = Projection()
    proj.apply(
        [
            AddGroup("G"),
            AddVertex("A", "G"),
            AddVertex("B", "G"),
            AddVertex("C"),                 # root-level vertex
            AddVertex("D"),                 # isolated vertex
            AddEdge("e0", "A", "B"),        # intra-group edge
            AddEdge("e1", "A", "C"),
            AddEdge("e2", "B", "C"),
            AddEdge("e3", "A", "C"),        # parallel multi-edge
            AddEdge("e4", "C", "A", 10, 20),  # active only on [10, 20)
        ]
    )

    print("== flat view (nothing collapsed) ==")
    show(proj)

    print("\n== collapse G: A->B stays internal, G->C merges 3 originals ==")
    proj.apply(Collapse("G"))
    show(proj)
    assert ("G", "G") not in proj.superedges(), "fake self-loop leaked!"
    assert proj.evidence("G", "C") == ["e1", "e2", "e3"]

    print("\n== half-open interval: e4 = C->A active on [10, 20) ==")
    print(f"  t=9 : C->G evidence = {proj.evidence('C', 'G', at=9)}")
    print(f"  t=10: C->G evidence = {proj.evidence('C', 'G', at=10)}")
    print(f"  t=20: C->G evidence = {proj.evidence('C', 'G', at=20)}")

    print("\n== expand, then collapse again: statistics conserved ==")
    before = (proj.superedges(), proj.internal_map())
    proj.apply(Expand("G"))
    show(proj)
    proj.apply(Collapse("G"))
    assert (proj.superedges(), proj.internal_map()) == before
    print("  conserved: OK")

    print("\n== atomic move: A leaves G (counts, multiplicities, reverse lookup) ==")
    proj.apply(MoveVertex("A", None))
    show(proj)
    print(f"  edge_location(e0) = {proj.edge_location('e0')}")
    print(f"  edge_location(e2) = {proj.edge_location('e2')}")

    print("\n== undo the move ==")
    proj.undo()
    assert proj.member_count("G") == 2 and proj.evidence("G", "C") == ["e1", "e2", "e3"]
    print("  restored: OK")

    print("\n== nested groups: H inside G, B inside H ==")
    proj.apply([AddGroup("H", "G"), MoveVertex("B", "H")])
    show(proj)
    proj.apply([Expand("G"), Collapse("H")])
    print("  after expanding G and collapsing H instead:")
    show(proj)
    proj.apply([Expand("H"), Collapse("G")])

    print("\n== named snapshots + independent forked view ==")
    proj.save_snapshot("G-collapsed")
    view = proj.fork()
    view.apply([Expand("G"), Collapse("H")])
    print("  fork view supernodes:", sorted(view.supernodes()))
    print("  original supernodes :", sorted(proj.supernodes()))
    proj.restore("G-collapsed")
    print("  restored snapshot   :", sorted(proj.supernodes()))

    print("\n== batch rejection: cycle and id conflict leave no trace ==")
    try:
        proj.apply([AddGroup("K", "H"), MoveGroup("G", "K")])
    except HierarchyCycleError as exc:
        print(f"  rejected cycle: {exc}")
    assert "K" not in proj.groups and proj.groups["G"] is None
    try:
        proj.apply([AddVertex("Z"), AddGroup("Z")])
    except IdConflictError as exc:
        print(f"  rejected id conflict: {exc}")
    assert "Z" not in proj.vertices and "Z" not in proj.groups

    print("\n== final cross-check vs brute-force re-projection ==")
    for at in (None, 5, 10, 20):
        brute = brute_projection(
            proj.vertices, proj.groups, proj.edges, proj.collapsed, at
        )
        assert proj.superedges(at) == brute["cross"], at
        assert proj.internal_map(at) == brute["internal"], at
    print("  incremental index == brute force at t in {None, 5, 10, 20}: OK")

    print("\nALL DEMO CHECKS PASSED")


if __name__ == "__main__":
    main()
