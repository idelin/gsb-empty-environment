"""Sample run: diamond graph, single-edge update, unreachable cycle,
dominance queries and path counterexamples."""
from dominator import DominatorTree, Graph


def show(dt: DominatorTree) -> None:
    for v in sorted(dt.graph.vertices):
        if dt.is_reachable(v):
            print(f"  idom({v}) = {dt.idom(v)}")
    print(f"  unreachable: {sorted(dt.unreachable)}")


def main() -> None:
    g = Graph()
    g.add_edge("e-sa", "S", "A")
    g.add_edge("e-sb", "S", "B")
    g.add_edge("e-at", "A", "T")
    g.add_edge("e-bt", "B", "T")
    g.add_edge("e-xy", "X", "Y")   # unreachable cycle
    g.add_edge("e-yx", "Y", "X")
    g.add_vertex("Z")              # isolated vertex

    dt = DominatorTree(g, "S")
    print("Diamond S->A/B->T (with unreachable cycle X<->Y and isolated Z):")
    show(dt)

    print("\nDominance queries:")
    print(f"  dominates(S, T) = {dt.dominates('S', 'T')}")
    print(f"  dominates(A, T) = {dt.dominates('A', 'T')}")
    print(f"  counterexample(A, T) = {dt.counterexample('A', 'T')}")
    print(f"  counterexample(S, T) = {dt.counterexample('S', 'T')}")

    print("\nRemove edge e-bt (B->T):")
    affected = dt.remove_edge("e-bt")
    print(f"  affected region: {sorted(affected)}")
    show(dt)
    print(f"  dominates(A, T) = {dt.dominates('A', 'T')}")
    print(f"  counterexample(A, T) = {dt.counterexample('A', 'T')}")

    print("\nTimed edge e-bt restored, active on [10, 20) only:")
    dt.add_edge("e-bt", "B", "T", start=10.0, end=20.0)
    for t in (9.999, 10.0, 19.999, 20.0):
        dt.set_time(t)
        print(f"  t={t:>6}: idom(T) = {dt.idom('T')}")


if __name__ == "__main__":
    main()
