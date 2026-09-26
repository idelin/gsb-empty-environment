"""End-to-end sample: synthetic integer/rational data, no external solver."""

from __future__ import annotations

from placement import (
    Problem,
    Status,
    apply_steps,
    build_migration_plan,
    placement_cost,
    solve,
)


def show(result, title):
    print(f"--- {title} ---")
    print(f"status          : {result.status.value}")
    print(f"nodes explored  : {result.nodes_explored}")
    if result.placement is not None:
        print(f"total cost      : {result.cost}")
        for oid, nids in sorted(result.placement.items()):
            print(f"  {oid}: {', '.join(nids)}")
    for diag in result.diagnostics:
        print(f"  diagnostic    : {diag}")
    print()


def build_main_problem() -> Problem:
    nodes = [
        ("n1", "r1", 10), ("n2", "r1", 6),
        ("n3", "r2", 10), ("n4", "r2", 6),
        ("n5", "r3", 10), ("n6", "r3", 6),
    ]
    objects = [
        ("A", 4, 3),  # 3 replicas -> must span all 3 racks
        ("B", 3, 2),
        ("C", 5, 2),  # fits only on 10-capacity nodes
        ("D", 2, 2),
    ]
    costs = {
        ("A", "n1"): 5, ("A", "n2"): 4, ("A", "n3"): 3,
        ("A", "n4"): 6, ("A", "n5"): 2, ("A", "n6"): 5,
        ("B", "n1"): 2, ("B", "n2"): 3, ("B", "n3"): 4,
        ("B", "n4"): 1, ("B", "n5"): 5, ("B", "n6"): 2,
        ("C", "n1"): 7, ("C", "n2"): 9, ("C", "n3"): 2,
        ("C", "n4"): 8, ("C", "n5"): 3, ("C", "n6"): 4,
        ("D", "n1"): 1, ("D", "n2"): 1, ("D", "n3"): 2,
        ("D", "n4"): 2, ("D", "n5"): 3, ("D", "n6"): "3/2",  # rational cost
    }
    return Problem.build(nodes, objects, costs, anti_affinity=[("A", "C")])


def main() -> None:
    problem = build_main_problem()

    # 1) Exact solve -> OPTIMAL
    optimal = solve(problem)
    show(optimal, "1. exact solve (OPTIMAL expected)")

    # 2) Tight search budget -> FEASIBLE but not proven optimal
    limited = solve(problem, node_limit=4)
    show(limited, "2. tiny search budget (FEASIBLE expected)")

    # 3) No search budget at all + a cost budget -> BUDGET_UNDECIDED
    undecided = solve(problem, budget=20, node_limit=0)
    show(undecided, "3. budget 20, no search budget (BUDGET_UNDECIDED expected)")

    # 4) Provably unattainable budget -> BUDGET_EXCEEDED
    exceeded = solve(problem, budget=20)
    show(exceeded, "4. budget 20, full search (BUDGET_EXCEEDED expected)")

    # 5) Insufficient capacity -> INFEASIBLE with diagnostics
    bad = Problem.build(
        nodes=[("m1", "r1", 8), ("m2", "r1", 8), ("m3", "r2", 4)],
        objects=[("E", 4, 3), ("F", 6, 2)],
        costs={(o, n): 1 for o in ("E", "F") for n in ("m1", "m2", "m3")},
    )
    show(solve(bad), "5. rack starvation + capacity deficit (INFEASIBLE expected)")

    # 6) Migration from a valid but expensive placement to the optimum
    current = {"A": ("n1", "n4", "n6"), "B": ("n3", "n5"),
               "C": ("n3", "n5"), "D": ("n2", "n3")}
    target = optimal.placement
    plan = build_migration_plan(problem, current, target)
    print("--- 6. migration plan (minimum replica count holds at every step) ---")
    print(f"current cost: {placement_cost(problem, current)}  ->  "
          f"target cost: {placement_cost(problem, target)}")
    for i, step in enumerate(plan, 1):
        print(f"  step {i:2d}: {step}")
    final = apply_steps(current, plan)
    assert final == {k: tuple(sorted(v)) for k, v in target.items()}
    moved = sum(
        problem.costs[(s.obj_id, s.node_id)] for s in plan if s.action == "add"
    )
    print(f"final placement matches optimum; transmission cost of moves: {moved}")


if __name__ == "__main__":
    main()
