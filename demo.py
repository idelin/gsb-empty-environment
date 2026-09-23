"""Runnable samples for the graphcolor package. Run: python3 demo.py"""

from graphcolor import (
    Graph,
    Solver,
    canonical_set,
    enumerate_all_colorings,
    find_conflict_core,
    interval_overlap_graph,
    solve_lex_min,
    verify_coloring,
)


def banner(title):
    print("\n" + "=" * 66)
    print(title)
    print("=" * 66)


def triangle():
    g = Graph()
    g.add_edge("e_ab", "a", "b")
    g.add_edge("e_bc", "b", "c")
    g.add_edge("e_ca", "c", "a")
    return g


banner("1. Triangle: 2 colors infeasible (proven), 3 colors feasible")
g = triangle()
r2 = Solver(g, 2).solve()
print(f"2 colors -> {r2.status} (search nodes: {r2.nodes})")
print("search trace (reproducible):")
for line in r2.trace[:8]:
    print("   ", line)
print("    ...")
r3 = Solver(g, 3, trace=False).solve()
print(f"3 colors -> {r3.status}: {r3.coloring}")
print("independent verifier:", verify_coloring(g, r3.coloring, 3) or "OK")

banner("2. Lexicographically minimal coloring")
lex = solve_lex_min(g, 3)
print(f"lex-min -> {lex.status}: {lex.coloring}")

banner("3. Precoloring conflict is located on the exact edge")
bad = Solver(g, 3, precoloring={"a": 0, "b": 0}, trace=False).solve()
print(f"precolor a=0,b=0 -> {bad.status}")
print("conflict:", bad.conflict.describe())

banner("4. Isolated vertices are kept and still bound by color capacity")
iso = Graph()
iso.add_vertex("lonely")
r = Solver(iso, 1, capacities={0: 0}, trace=False).solve()
print(f"1 isolated vertex, capacity 0 -> {r.status}")
print("conflict:", r.conflict.describe())

banner("5. Multiedges preserved; self-loop infeasible")
multi = Graph()
multi.add_edge("m1", "a", "b")
multi.add_edge("m2", "a", "b")
print("edges:", [(e.id, e.u, e.v) for e in multi.edges])
loop = Graph()
loop.add_edge("loop1", "a", "a")
rl = Solver(loop, 5, trace=False).solve()
print(f"self-loop -> {rl.status}; edges in conflict: {rl.conflict.edge_ids()}")

banner("6. Half-open time intervals [start, end)")
iv = interval_overlap_graph({"i1": (0, 1), "i2": (1, 2), "i3": (0.5, 1.5)})
print("overlap edges:", [e.id for e in iv.edges], "(i1/i2 touch at t=1: no edge)")
ri = Solver(iv, 2, trace=False).solve()
print(f"2 colors -> {ri.status}: {ri.coloring}")

banner("7. Minimal conflict core of original constraints")
k4 = Graph()
for i in range(4):
    for j in range(i + 1, 4):
        k4.add_edge(f"e{i}{j}", f"v{i}", f"v{j}")
k4.add_edge("e_extra", "x", "y")
core = find_conflict_core(k4, 3)
print(core.describe())
core_tight_budget = find_conflict_core(
    triangle(), 3, precoloring={"a": 0, "b": 0}, budget=0
)
print(core_tight_budget.describe())

banner("8. Budget exhausted != infeasible")
k5 = Graph()
for i in range(5):
    for j in range(i + 1, 5):
        k5.add_edge(f"e{i}{j}", f"v{i}", f"v{j}")
rb = Solver(k5, 4, budget=1, trace=False).solve()
print(f"K5 with 4 colors, budget=1 -> {rb.status}")
rf = Solver(k5, 4, budget=100_000, trace=False).solve()
print(f"K5 with 4 colors, ample budget -> {rf.status}")

banner("9. Symmetry breaking on/off vs independent enumeration")
square = Graph()
square.add_edge("e1", "a", "b")
square.add_edge("e2", "b", "c")
square.add_edge("e3", "c", "d")
square.add_edge("e4", "d", "a")
vertices = sorted(square.vertices)
brute, _ = enumerate_all_colorings(square, 3)
off, _ = Solver(square, 3, symmetry_breaking=False, trace=False).enumerate_solutions()
on, _ = Solver(square, 3, symmetry_breaking=True, trace=False).enumerate_solutions()
print(f"brute-force solutions: {len(brute)}; solver sym-off: {len(off)}; "
      f"solver sym-on: {len(on)}")
print("sym-off set == brute-force set:",
      {tuple(s[v] for v in vertices) for s in off}
      == {tuple(s[v] for v in vertices) for s in brute})
print("sym-on classes == brute-force classes:",
      canonical_set(on, vertices) == canonical_set(brute, vertices))
print("canonical classes:", sorted(canonical_set(brute, vertices)))
