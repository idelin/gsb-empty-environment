"""Runnable samples for the synthetic asymmetric CVRPTW solver."""

from fractions import Fraction

from cvrptw import Instance, Status, solve_exact, solve_heuristic


def show(title, res):
    print(f"== {title} ==")
    print(f"status    : {res.status.value}")
    print(f"objective : {res.objective if res.objective is None else res.objective}")
    print(f"routes    : {res.routes}")
    print(f"nodes     : {res.nodes}  method={res.method}")
    print(f"detail    : {res.detail}")
    if res.report:
        print("per-segment verification:")
        for line in res.report:
            print("  " + line)
    print()


def sample_instance():
    # 4 customers, asymmetric distances, rational entries, tight-ish windows.
    H = Fraction(1, 2)
    dist = [
        [0, 2, 4 + H, 3, 7],
        [5, 0, 2, 8, 3],
        [2, 3, 0, 2, 9],
        [4, 9, 3, 0, 2],
        [3, 2, 8, 4, 0],
    ]
    return Instance.from_lists(
        dist=dist,
        demand=[0, 3, 4, 2, 3],   # total 12 > capacity 10 -> at least 2 routes
        capacity=10,
        early=[0, 0, 3, 0, 5],
        late=[60, 20, 25, 30, 40],
        service=[0, 1, 2, 1, 2],
    )


def main():
    inst = sample_instance()

    show("A1 exact solve (proven optimal)", solve_exact(inst))

    full_nodes = solve_exact(inst).nodes
    show("A2 exact solve, node budget = 1 (budget undecided)",
         solve_exact(inst, node_limit=1))
    feasible_limit = next(
        lim for lim in range(2, full_nodes)
        if solve_exact(inst, node_limit=lim).status is Status.FEASIBLE
    )
    show(f"A3 exact solve, node budget = {feasible_limit} "
         f"(feasible, optimality unproven)", solve_exact(inst, node_limit=feasible_limit))

    show("A4 heuristic solve (feasible, not proven)", solve_heuristic(inst))

    infeasible_cap = Instance.from_lists(
        dist=[[0, 1, 1], [1, 0, 1], [1, 1, 0]],
        demand=[0, 6, 6], capacity=10,
        early=[0, 0, 0], late=[50, 50, 50], service=[0, 0, 0],
        max_vehicles=1,
    )
    show("B exact solve (infeasible: total demand > one vehicle)",
         solve_exact(infeasible_cap))

    unreachable = Instance.from_lists(
        dist=[[0, 3], [3, 0]],
        demand=[0, 1], capacity=10,
        early=[0, 0], late=[50, 2], service=[0, 0],
    )
    show("C1 exact solve (infeasible: window unreachable)", solve_exact(unreachable))
    show("C2 heuristic solve (not found, distinct from infeasible)",
         solve_heuristic(unreachable))


if __name__ == "__main__":
    main()
