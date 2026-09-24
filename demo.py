"""Runnable samples for the graphcolor package. Run: python3.11 demo.py"""

from graphcolor import (
    Graph, Problem, Solver, Status, compare_with_solver, minimal_conflict_core,
    trace_digest, verify,
)


def show_trace(trace, limit=14):
    for e in trace[:limit]:
        print(f"    [{e.interval.start:>3.0f},{e.interval.end:<3.0f}) {e.kind:<9} {e.detail}")
    if len(trace) > limit:
        print(f"    ... ({len(trace) - limit} more events)")


def triangle(colors, **kw):
    g = Graph()
    g.add_edge("e-ab", "a", "b")
    g.add_edge("e-bc", "b", "c")
    g.add_edge("e-ac", "a", "c")
    return Problem(g, tuple(colors), **kw)


def main():
    print("== 1. Triangle with 2 colors: proven infeasible + minimal core ==")
    p = triangle(["red", "green"])
    r = Solver(p).solve()
    print(f"  status: {r.status.value} (nodes={r.nodes})")
    core = minimal_conflict_core(p)
    print(f"  core: {[str(c) for c in core.core]}  subset-minimal: {core.is_subset_minimal}")

    print("== 2. Triangle with 3 colors: lexicographically smallest coloring ==")
    p = triangle(["red", "green", "blue"])
    r = Solver(p).solve()
    print(f"  status: {r.status.value}  coloring: {r.coloring}")
    print(f"  verifier violations: {verify(p, r.coloring)}")
    print(f"  trace digest: {trace_digest(r.trace)[:16]}...")
    show_trace(r.trace)

    print("== 3. Precoloring conflict: the offending edge is located directly ==")
    p = triangle(["red", "green"], precoloring={"a": "red", "c": "red"})
    r = Solver(p).solve()
    print(f"  status: {r.status.value}  conflict: {r.conflict.note}")

    print("== 4. Isolated vertex still bounded by color capacity ==")
    g = Graph()
    g.add_edge("e1", "x", "y")
    g.add_vertex("lonely")
    p = Problem(g, ("red", "green"), capacities={"red": 1, "green": 1})
    r = Solver(p).solve()
    print(f"  3 vertices / capacity 2 -> status: {r.status.value}")
    core = minimal_conflict_core(p)
    print(f"  core: {[str(c) for c in core.core]}  subset-minimal: {core.is_subset_minimal}")

    print("== 5. Budget exhaustion is not infeasibility ==")
    p = triangle(["red", "green"])
    r = Solver(p, max_nodes=1).solve()
    print(f"  status with max_nodes=1: {r.status.value}")
    core = minimal_conflict_core(p, max_nodes_per_check=1)
    print(f"  core status: {core.status.value}, minimal claimed: {core.is_subset_minimal}")
    print(f"  notes: {core.notes}")

    print("== 6. Retractable assumptions ==")
    p = triangle(["red", "green", "blue"])
    s = Solver(p)
    print(f"  assume a=blue: {s.push_assumption('a', 'blue')}")
    r = s.solve()
    print(f"  solve under assumption: {r.status.value} {r.coloring}")
    s.pop_assumption()
    r = s.solve()
    print(f"  after retract:          {r.status.value} {r.coloring}")

    print("== 7. Symmetry breaking on/off vs brute force (multi-edge + isolated) ==")
    g = Graph()
    g.add_edge("m1", "u", "v")
    g.add_edge("m2", "u", "v")  # multi-edge
    g.add_vertex("w")           # isolated vertex
    p = Problem(g, ("c1", "c2"), capacities={"c1": 2, "c2": 2})
    rep = compare_with_solver(p)
    print(f"  legal colorings: {rep.num_legal}, symmetry classes: {rep.num_classes}")
    print(f"  solver agrees with enumeration: {rep.ok}")


if __name__ == "__main__":
    main()
