"""Demo run: asymmetric CVRPTW on synthetic integer/rational data.

Run with:  python3.11 main.py
"""

from fractions import Fraction

from cvrptw import (Customer, Problem, Status, format_verification,
                    solve_exact, solve_heuristic, verify_routes)


def build_problem() -> Problem:
    # Node order: 0=DEPOT, 1=A, 2=B, 3=C, 4=D, 5=E.
    # Deliberately asymmetric: d(i,j) != d(j,i) in many places.
    distances = [
        [0, 2, 6, 3, 7, 4],
        [9, 0, 2, 8, 3, 6],
        [2, 7, 0, 5, 9, 1],
        [4, 3, 6, 0, 2, 8],
        [3, 8, 4, 6, 0, 2],
        [8, 2, 3, 7, 5, 0],
    ]
    customers = (
        Customer("A", demand=3, ready=0, due=20, service=1),
        Customer("B", demand=4, ready=5, due=15, service=1),
        Customer("C", demand=3, ready=0, due=30, service=Fraction(1, 2)),
        Customer("D", demand=2, ready=8, due=18, service=1),
        Customer("E", demand=4, ready=0, due=25, service=1),
    )
    return Problem(capacity=8, n_vehicles=2, customers=customers,
                   distances=distances, horizon=40)


def show(title, result):
    print(f"== {title} ==")
    print(f"status: {result.status.value}")
    print(f"objective (actual total distance): {result.objective}")
    print(f"routes (customer indices): {result.routes}")
    print(f"nodes explored: {result.nodes_explored}")
    print(f"note: {result.message}")
    print()


def main():
    problem = build_problem()

    exact = solve_exact(problem)
    show("exact branch-and-bound", exact)
    if exact.routes:
        ver = verify_routes(problem, exact.routes)
        print("per-leg arrival / load verification (exact solution):")
        print(format_verification(problem, ver))
        print()

    heur = solve_heuristic(problem)
    show("greedy heuristic", heur)

    limited = solve_exact(problem, node_budget=5)
    show("exact search with tiny node budget (5)", limited)

    infeasible = Problem(capacity=8, n_vehicles=1,
                         customers=(Customer("X", demand=9, ready=0, due=10, service=0),),
                         distances=[[0, 1], [1, 0]], horizon=10)
    show("provably infeasible instance (demand > capacity)",
         solve_exact(infeasible))


if __name__ == "__main__":
    main()
