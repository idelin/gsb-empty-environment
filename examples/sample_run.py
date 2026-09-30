"""Runnable demo: optimal solve, budget interruption, infeasible vs unknown.

Run from the repository root:  python3 examples/sample_run.py
"""
import os
import random
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from fdsolver import Problem, Status, solve
from checker import check_assignment


def demo_optimal():
    print("=" * 64)
    print("1) Small problem solved to PROVEN optimality")
    print("=" * 64)
    # Choose items to cover demand at minimum cost:
    #   min 3a + 2b + 4c  s.t.  a + b + c >= 5,  a + 2c >= 4
    p = Problem(
        domains={"a": range(6), "b": range(6), "c": range(6)},
        objective={"a": 3, "b": 2, "c": 4},
        constraints=[
            ({"a": 1, "b": 1, "c": 1}, ">=", 5),
            ({"a": 1, "c": 2}, ">=", 4),
        ],
    )
    r = solve(p)
    print(r)
    assert r.status is Status.OPTIMAL
    assert check_assignment(p, r.assignment).ok
    print()


def demo_budget_interrupt():
    print("=" * 64)
    print("2) Larger problem, tight node budget -> best known solution + gap")
    print("   (status FEASIBLE: optimality is NOT claimed)")
    print("=" * 64)
    rng = random.Random(2024)
    n = 12
    domains = {f"x{i}": list(range(0, 5)) for i in range(n)}
    objective = {f"x{i}": rng.randint(-4, 6) for i in range(n)}
    constraints = []
    for _ in range(6):
        coeffs = {f"x{i}": rng.randint(1, 3)
                  for i in rng.sample(range(n), 6)}
        constraints.append((coeffs, ">=", rng.randint(8, 20)))
    p = Problem(domains, objective, constraints)

    r = solve(p, max_nodes=400)
    print(r)
    if r.assignment is not None:
        rep = check_assignment(p, r.assignment)
        print(f"independent re-check of returned solution: {rep}")
    print()

    print("-- same problem, unlimited budget (proof of optimality) --")
    full = solve(p)
    print(full)
    if r.assignment is not None:
        print(f"note: interrupted UB={r.objective_value} vs proven "
              f"optimum={full.objective_value}; reported LB "
              f"{r.lower_bound} <= optimum holds: "
              f"{r.lower_bound <= full.objective_value}")
    print()


def demo_infeasible_vs_unknown():
    print("=" * 64)
    print("3) 'Proven infeasible' vs 'no solution found yet' are distinct")
    print("=" * 64)
    bad = Problem({"x": range(4), "y": range(4)},
                  {"x": 1},
                  [({"x": 1, "y": 1}, "<=", 1),
                   ({"x": 1, "y": 1}, ">=", 3)])
    print("-- contradictory constraints --")
    print(solve(bad))
    print()
    hard = Problem({f"x{i}": list(range(5)) for i in range(8)},
                   {f"x{i}": 1 for i in range(8)},
                   [({f"x{i}": 1 for i in range(8)}, ">=", 3)])
    print("-- feasible instance, zero node budget --")
    r = solve(hard, max_nodes=0)
    print(r)
    assert r.status is Status.UNKNOWN
    print()


if __name__ == "__main__":
    demo_optimal()
    demo_budget_interrupt()
    demo_infeasible_vs_unknown()
