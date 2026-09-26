"""Runnable demo: python3.11 example.py"""

from set_solver import Conflict, InterEq, Solver, SubsetOf, UnionEq


def demo_satisfiable():
    print("=== Satisfiable problem (solve + independent verification) ===")
    solver = Solver()
    solver.add_var("A", upper={1, 2, 3, 4}, min_card=1)
    solver.add_var("B", upper={2, 3, 4, 5}, max_card=2)
    solver.add_var("C", upper={1, 2, 3, 4, 5})
    solver.add_var("D", upper={1, 2, 3, 4, 5})
    solver.add_constraint(UnionEq(solver.vars["A"], solver.vars["B"], solver.vars["C"]))
    solver.add_constraint(InterEq(solver.vars["A"], solver.vars["B"], solver.vars["D"]))
    solver.add_constraint(SubsetOf(solver.vars["D"], solver.vars["B"]))
    solution = solver.solve()  # internally re-checked by check_solution
    print("solution:", {k: sorted(v) for k, v in solution.items()})
    print("C == A | B:", solution["C"] == solution["A"] | solution["B"])
    print("D == A & B:", solution["D"] == solution["A"] & solution["B"])
    print()


def demo_intersection_bounds():
    print("=== Intersection: lower and upper computed separately ===")
    solver = Solver()
    solver.add_var("A", upper={1, 2}, lower={1})
    solver.add_var("B", upper={1, 2, 3}, lower={2})
    c = solver.add_var("C", upper={1, 2, 3})
    solver.add_constraint(InterEq(solver.vars["A"], solver.vars["B"], c))
    solver.propagate()
    print("C must contain (lower):", sorted(c.lower), " <- possible members NOT promoted")
    print("C may contain  (upper):", sorted(c.upper))
    print()


def demo_cardinality_conflict():
    print("=== Conflict: must contain 3 elements, max cardinality 2 ===")
    solver = Solver()
    try:
        solver.add_var("S", upper={1, 2, 3, 4}, lower={1, 2, 3}, max_card=2)
    except Conflict as exc:
        print("Conflict detected:", exc)
    print()


def demo_unsat():
    print("=== Unsatisfiable: union result capped at 0 but A requires 1 ===")
    solver = Solver()
    solver.add_var("A", upper={1, 2}, lower={1})
    solver.add_var("B", upper={2})
    solver.add_var("C", upper={1, 2}, max_card=0)
    solver.add_constraint(UnionEq(solver.vars["A"], solver.vars["B"], solver.vars["C"]))
    print("solve() ->", solver.solve())


if __name__ == "__main__":
    demo_satisfiable()
    demo_intersection_bounds()
    demo_cardinality_conflict()
    demo_unsat()
