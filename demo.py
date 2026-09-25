"""End-to-end demo: batch input -> exact plan -> phased execution.

Run: python3.11 demo.py
"""
from migration import (Batch, Column, Executor, Phase, StaleAckError, Type,
                       build_plan, problem_from_batches, slot_loads, solve,
                       verify)


def show_loads(title, loads, capacities):
    print(title)
    for sid in sorted(loads):
        print(f"  {sid}: {loads[sid]:>4} / {capacities[sid]} bytes")
    print(f"  max load: {max(loads.values())}")


def main():
    partitions = Batch([
        Column("pid", Type.STRING, ["p0", "p1", "p2", "p3", "p4", "p5"]),
        Column("weight", Type.INT64, [50, 40, 35, 30, 25, 20]),
        Column("slot", Type.STRING, ["s0", "s0", "s1", "s1", "s2", "s2"]),
        # null (None) and NaN are distinct values in a FLOAT64 column:
        Column("score", Type.FLOAT64, [1.5, None, float("nan"), 0.0, 2.5, None]),
    ])
    slots = Batch([
        Column("sid", Type.STRING, ["s0", "s1", "s2"]),
        Column("capacity", Type.INT64, [120, 120, 120]),
    ])
    anti = Batch([
        Column("left", Type.STRING, ["p0"]),
        Column("right", Type.STRING, ["p5"]),
    ])

    print("null vs NaN in column 'score':")
    for i in range(partitions.num_rows):
        v = partitions.value("score", i)
        tag = "null" if partitions.is_null("score", i) else (
            "NaN" if partitions.is_nan("score", i) else "value")
        print(f"  row {i}: {v!r} -> {tag}")
    print()

    problem = problem_from_batches(partitions, slots, budget=200, anti_affinity=anti)
    capacities = problem.capacities
    weights = problem.weights

    show_loads("before:", slot_loads(problem.initial, weights, capacities), capacities)
    print()

    result = solve(problem)
    print(f"solver: status={result.status} nodes={result.nodes}")
    print(f"  max_load={result.max_load} moved_bytes={result.moved_bytes}")
    print(f"  proven lower_bound={result.lower_bound} gap={result.gap}")
    errors = verify(problem, result.assignment)
    print(f"  independent verification: {'OK' if not errors else errors}")
    print()

    show_loads("after (planned):", slot_loads(result.assignment, weights, capacities), capacities)
    print()

    plan = build_plan(problem, result, plan_id="plan-1", generation=1)
    print(f"deterministic plan {plan.plan_id} (generation {plan.generation}):")
    for mv in plan.moves:
        print(f"  move {mv.pid}: {mv.src} -> {mv.dst} ({mv.weight} bytes)")
    print(f"  total moved: {plan.moved_bytes} bytes (budget {problem.budget})")
    print()

    initial = {pid: (sid, weights[pid]) for pid, sid in problem.initial.items()}
    ex = Executor(capacities, initial, budget=problem.budget)
    report = ex.execute(plan)
    print(f"execute: success={report.success} rolled_back={report.rolled_back}")
    for step in report.steps:
        print(f"  {step}")
    print(f"  budget spent={report.budget_spent} remaining={report.budget_remaining}")
    show_loads("after (executed):", ex.owned_loads(), capacities)
    print()

    print("replay same plan (idempotent):")
    replay = ex.execute(plan)
    print(f"  success={replay.success} budget spent now={ex.budget_spent}")
    print(f"  steps: {len(replay.steps)} (all skipped)")
    print()

    print("stale ack rejection:")
    try:
        ex.ack("p1", generation=0)
    except StaleAckError as e:
        print(f"  rejected: {e}")
    print(f"  current-generation ack accepted: {ex.ack('p1', generation=1)}")
    print()

    print("failure + step-by-step rollback:")
    ex2 = Executor(capacities, dict(initial), budget=problem.budget)
    bad = ex2.execute(plan, fail_at=(Phase.SWITCH, plan.moves[-1].pid))
    print(f"  success={bad.success} rolled_back={bad.rolled_back}")
    for step in bad.steps:
        print(f"    {step}")
    print(f"  budget after rollback: spent={ex2.budget_spent} "
          f"remaining={ex2.budget_remaining} (no leak)")
    print(f"  owners restored: {ex2.owners_snapshot() == {p: s for p, (s, w) in initial.items()}}")
    print()

    print("oversized partition cannot be split:")
    big = Batch([
        Column("pid", Type.STRING, ["q0", "q1"]),
        Column("weight", Type.INT64, [250, 30]),
        Column("slot", Type.STRING, ["t0", "t1"]),
    ])
    tslots = Batch([
        Column("sid", Type.STRING, ["t0", "t1"]),
        Column("capacity", Type.INT64, [200, 100]),
    ])
    hard = problem_from_batches(big, tslots, budget=1000)
    res = solve(hard)
    print(f"  status={res.status} unmet_bytes={res.unmet_bytes} ({res.detail})")


if __name__ == "__main__":
    main()
