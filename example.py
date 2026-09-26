"""Runnable demo of the DPLL solver: graph coloring, assumptions, pigeonhole."""

from sat_solver import FiniteDomainEncoder, Solver, Status, check_model


def separator(title):
    print("\n" + "=" * 66)
    print(title)
    print("=" * 66)


def graph_coloring_demo():
    separator("1. Graph 3-coloring (finite-domain vars) + assumptions/retraction")
    # Two triangles sharing vertex 2:  0-1-2-0  and  2-3-4-2
    edges = [(0, 1), (1, 2), (2, 0), (2, 3), (3, 4), (4, 2)]
    colors = ["red", "green", "blue"]
    solver = Solver()
    enc = FiniteDomainEncoder(solver)
    for v in range(5):
        enc.new_variable(f"v{v}", colors)
    for a, b in edges:  # adjacent vertices differ
        for c in colors:
            solver.add_clause([-enc.literal(f"v{a}", c), -enc.literal(f"v{b}", c)])

    res = solver.solve()
    print(f"status: {res.status.value} (verified={res.verified})")
    coloring = {f"v{v}": enc.decode(f"v{v}", res.model) for v in range(5)}
    print("coloring:", coloring)
    print("explanation:", res.explanation)

    # Assumption: force adjacent v0 and v1 to the same color -> must be UNSAT.
    bad = [enc.literal("v0", "red"), enc.literal("v1", "red")]
    res2 = solver.solve(assumptions=bad)
    print(f"\nassuming v0=v1=red -> {res2.status.value}")
    print("explanation:", res2.explanation)

    # Assumptions were retracted automatically: solver is reusable.
    res3 = solver.solve(assumptions=[enc.literal("v0", "red")])
    print(f"\nafter retraction, assuming only v0=red -> {res3.status.value}")
    print("coloring:", {f"v{v}": enc.decode(f"v{v}", res3.model) for v in range(5)})


def pigeonhole_demo():
    separator("2. Pigeonhole: 4 pigeons, 3 holes (UNSAT, exhaustive search)")
    pigeons, holes = 4, 3
    solver = Solver()
    var = lambda p, h: p * holes + h + 1
    for p in range(pigeons):
        solver.add_clause([var(p, h) for h in range(holes)])
    for h in range(holes):
        for p1 in range(pigeons):
            for p2 in range(p1 + 1, pigeons):
                solver.add_clause([-var(p1, h), -var(p2, h)])
    res = solver.solve()
    print(f"status: {res.status.value}")
    print("explanation:", res.explanation)


def edge_cases_demo():
    separator("3. Edge cases")
    r1 = Solver().solve()
    print(f"empty formula          -> {r1.status.value} (verified={r1.verified})")

    s2 = Solver()
    s2.add_clause([1, -1, 2])  # tautology: ignored
    r2 = s2.solve()
    print(f"tautology-only formula -> {r2.status.value}, "
          f"working clauses kept: {s2.n_working_clauses}")

    s3 = Solver()
    s3.add_clause([1])
    s3.add_clause([])
    r3 = s3.solve()
    print(f"empty clause           -> {r3.status.value}: {r3.explanation}")

    s4 = Solver()
    s4.add_clause([1])
    s4.add_clause([-1, 2])
    s4.add_clause([-2, 3])
    r4 = s4.solve()
    chain = {v: r4.model[v] for v in (1, 2, 3)}
    print(f"unit propagation chain -> {r4.status.value}, model {chain}, "
          f"independent check: {check_model(s4.original_clauses, r4.model)}")


if __name__ == "__main__":
    graph_coloring_demo()
    pigeonhole_demo()
    edge_cases_demo()
