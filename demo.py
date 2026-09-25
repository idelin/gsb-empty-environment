"""Runnable demo: python3.11 demo.py"""

from fractions import Fraction

from booldag import Graph, IntervalSet, ivl, oracle, point


def show(node):
    state = "UNKNOWN" if node.is_unknown() else str(node.value)
    print(f"  {node.name:<5} = {state}")


def main():
    g = Graph()

    print("== [0,1] ∪ (1,2] : connected union, empty intersection ==")
    a, b = g.leaf("a"), g.leaf("b")
    a.set(ivl(0, 1))                  # [0,1]
    b.set(ivl(1, 2, False, True))     # (1,2]
    u = g.node("u", "union", ["a", "b"])
    i = g.node("i", "intersect", ["a", "b"])
    d = g.node("d", "diff", ["u", "b"])
    show(u)
    print(f"  u connected? {u.is_connected()}   i empty? {i.is_empty()}")
    show(d)                           # [0,2] \ (1,2] = [0,1]
    print(f"  d contains 1? {d.contains(1)}   d contains 3/2? {d.contains(Fraction(3, 2))}")

    print("\n== provenance: minimal source proof & back-reference ==")
    print(f"  prove(u, 1/2) -> {sorted(g.prove(u, Fraction(1, 2)))}")
    print(f"  prove(u, 3/2) -> {sorted(g.prove(u, Fraction(3, 2)))}")
    print(f"  prove(d, 1)   -> {sorted(g.prove(d, 1))}")

    print("\n== same leaf via two paths, retracted once ==")
    g2 = Graph()
    src = g2.leaf("src")
    na = g2.node("na", "union", ["src"])
    nb = g2.node("nb", "union", ["src"])
    top = g2.node("top", "union", ["na", "nb"])
    src.set(ivl(0, 1))
    print(f"  before retract: top = {top.value}, prove(1/2) = {sorted(g2.prove(top, Fraction(1/2)))}")
    src.clear()
    print(f"  after  retract: top = {top.value}, prove(1/2) = {g2.prove(top, Fraction(1, 2))}")

    print("\n== transaction: batch commit and rollback ==")
    snap = g.snapshot()
    with g.transaction():
        a.set(ivl(0, 5))
        b.clear()
    show(u)
    print(f"  snapshot still sees: {snap.value('u')}")
    with g.transaction() as tx:
        a.set(ivl(9, 9))
        tx.rollback()
    show(u)

    print("\n== unknown vs empty vs isolated point ==")
    g3 = Graph()
    unk = g3.leaf("unk")
    emp = g3.leaf("emp")
    emp.clear()
    pt = g3.leaf("pt")
    pt.set(point(Fraction(7, 2)))
    w = g3.node("w", "union", ["unk", "emp", "pt"])
    show(w)                            # UNKNOWN propagates
    unk.set(ivl(0, 3))
    show(w)                            # [0,3] | {7/2}
    print(f"  isolated points of w: {[str(p) for p in w.value.isolated_points()]}")

    print("\n== independent oracle cross-check ==")
    for node in (u, i, d):
        n_pts = oracle.check_node(g, node)
        print(f"  {node.name}: OK ({n_pts} probe points)")
    print(f"  w: OK ({oracle.check_node(g3, w)} probe points)")


if __name__ == "__main__":
    main()
