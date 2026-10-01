"""Runnable demo:  python3.11 example.py

Shows three scenarios:
  1. a problem solved to proven optimality,
  2. a budget-interrupted run returning the best known solution + gap,
  3. a provably infeasible problem vs. an undecided (UNKNOWN) one.
"""
from model import LinearConstraint, Problem, Sense
from solver import solve


def show(title, result):
    print(f"--- {title} ---")
    print(f"status         : {result.status.value}")
    print(f"interrupted    : {result.interrupted}")
    print(f"solution       : {result.solution}")
    print(f"objective (ub) : {result.upper_bound}")
    print(f"lower bound    : {result.lower_bound}")
    print(f"abs gap        : {result.gap}")
    rel = result.relative_gap
    print(f"rel gap        : {rel:.2%}" if rel is not None else "rel gap        : n/a")
    print(f"nodes explored : {result.nodes_explored}")
    print()


# 1. Small integer program, solved to optimality.
p1 = Problem(
    domains=[range(0, 6)] * 5,
    objective=(3, 5, 2, 4, 1),
    constraints=[
        LinearConstraint((1, 2, 0, 1, 3), Sense.GE, 9),
        LinearConstraint((2, 0, 1, 0, 1), Sense.LE, 8),
        LinearConstraint((0, 1, 1, 1, 0), Sense.GE, 4),
    ],
)
show("solved to optimality", solve(p1))

# 2. Larger problem with a tiny node budget: interrupted, but we still
#    get the best known solution and a reliable lower bound / gap.
p2 = Problem(
    domains=[range(0, 7)] * 9,
    objective=(7, 3, 9, 2, 5, 8, 1, 6, 4),
    constraints=[
        LinearConstraint((1,) * 9, Sense.GE, 20),
        LinearConstraint((2, 1, 0, 3, 1, 0, 2, 1, 1), Sense.LE, 18),
        LinearConstraint((0, 2, 1, 1, 0, 1, 3, 0, 1), Sense.GE, 8),
    ],
)
show("interrupted by node budget (500 nodes)", solve(p2, max_nodes=500))
show("same problem, unlimited budget", solve(p2))

# 3a. Provably infeasible: x in {0..3} but x >= 5 is required.
p3 = Problem(
    domains=[range(0, 4)],
    objective=(1,),
    constraints=[LinearConstraint((1,), Sense.GE, 5)],
)
show("provably infeasible", solve(p3))

# 3b. Budget too small to find or disprove anything: UNKNOWN.
p4 = Problem(
    domains=[range(0, 20)] * 10,
    objective=(1,) * 10,
    constraints=[LinearConstraint((1,) * 10, Sense.GE, 150)],
)
show("undecided within budget (UNKNOWN)", solve(p4, max_nodes=3))
