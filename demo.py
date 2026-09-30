"""Runnable examples for the prefix-compressed tuple-table CSP solver."""

from checker import brute_force_solutions, verify_solution
from csp import CSP, Solver, solve
from tuple_table import TupleTable


def demo_prefix_compression() -> None:
    print("== 1. Shared-prefix compression & duplicate tuples ==")
    raw = [(0, 0, 1), (0, 0, 2), (0, 1, 1), (1, 0, 1),
           (0, 0, 1), (0, 0, 1)]          # duplicates included on purpose
    table = TupleTable(3, raw)
    print(f"   input tuples (with duplicates): {len(raw)}")
    print(f"   unique tuples stored:           {len(table)}")
    print(f"   flat cells would be:            {len(raw) * 3}")
    print(f"   trie nodes used:                {table.node_count()}")
    print(f"   (0,0,1) in table: {(0, 0, 1) in table}, "
          f"(1,1,1) in table: {(1, 1, 1) in table}")
    print()


def demo_support_maintenance() -> None:
    print("== 2. Support maintenance after domain value removal ==")
    table = TupleTable(2, [(0, 0), (0, 1), (1, 1), (2, 2)])
    trail: list = []
    print(f"   supported at pos 0 initially: "
          f"{sorted(table.supported_values(0))}")
    table.remove_value(0, 0, trail)
    print(f"   removed value 0 from pos 0 -> valid tuples: "
          f"{table.valid_count()}")
    print(f"   value 1 at pos 1 still supported? "
          f"{table.has_support(1, 1)} (via tuple (1,1))")
    table.remove_value(0, 1, trail)
    print(f"   removed value 1 from pos 0 -> value 1 at pos 1 supported? "
          f"{table.has_support(1, 1)}")
    while trail:
        entry = trail.pop()
        if entry[0] == "tuple":
            entry[1].restore_tuple(entry[2])
    print(f"   after undo: valid tuples = {table.valid_count()}, "
          f"pos-0 supports = {sorted(table.supported_values(0))}")
    print()


def demo_positive_table() -> None:
    print("== 3. Positive table: solve + independent verification ==")
    csp = CSP()
    for v in "xyz":
        csp.add_variable(v, {0, 1, 2})
    csp.add_constraint(["x", "y", "z"],
                       [(0, 0, 1), (0, 0, 2), (0, 1, 1), (2, 2, 0)])
    solution = solve(csp)
    print(f"   solution: {solution}")
    print(f"   independent checker: {verify_solution(csp, solution)}")
    print(f"   total solutions (brute force): "
          f"{sum(1 for _ in brute_force_solutions(csp))}")
    print()


def demo_negative_table() -> None:
    print("== 4. Negative table: 3-coloring a triangle ==")
    csp = CSP()
    colors = {"r", "g", "b"}
    for v in "abc":
        csp.add_variable(v, colors)
    forbidden = [(c, c) for c in colors]
    for u, v in [("a", "b"), ("b", "c"), ("a", "c")]:
        csp.add_constraint([u, v], forbidden, polarity=TupleTable.NEGATIVE)
    solution = solve(csp)
    print(f"   coloring: {solution}")
    print(f"   independent checker: {verify_solution(csp, solution)}")
    print(f"   total colorings (brute force): "
          f"{sum(1 for _ in brute_force_solutions(csp))}")
    print()


def demo_edge_cases() -> None:
    print("== 5. Edge cases: empty tables ==")
    csp = CSP()
    csp.add_variable("x", {1, 2})
    csp.add_constraint(["x"], [])                      # empty positive
    print(f"   empty POSITIVE table -> solve() = {solve(csp)}  (infeasible)")

    csp = CSP()
    csp.add_variable("x", {1, 2})
    csp.add_variable("y", {1, 2})
    csp.add_constraint(["x", "y"], [], polarity=TupleTable.NEGATIVE)
    n = sum(1 for _ in brute_force_solutions(csp))
    print(f"   empty NEGATIVE table -> no restriction, "
          f"{n} assignments all valid; solve() = {solve(csp)}")
    print()


def demo_domain_removal_propagation() -> None:
    print("== 6. Domain removal cascades through supports ==")
    csp = CSP()
    csp.add_variable("x", {0, 1})
    csp.add_variable("y", {0, 1})
    csp.add_constraint(["x", "y"], [(0, 0), (1, 1)])
    solver = Solver(csp)
    solver.prune("x", 0)          # user/algorithm deletes 0 from D(x)
    solver.propagate()
    print(f"   after removing 0 from D(x): "
          f"D(x)={sorted(solver.domains['x'])}, "
          f"D(y)={sorted(solver.domains['y'])}  (y=0 lost its support)")
    print()


if __name__ == "__main__":
    demo_prefix_compression()
    demo_support_maintenance()
    demo_positive_table()
    demo_negative_table()
    demo_edge_cases()
    demo_domain_removal_propagation()
