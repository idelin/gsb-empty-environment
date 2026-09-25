"""End-to-end demo: typed batches -> exact solve -> plan -> phased execute.

Run with:  python3.11 demo.py
"""

from typed_batch import ColType, NULL, TypedBatch, group_sum_int, is_nan, sum_float
from model import Cluster, Partition, Slot
from solver import solve, verify_assignment
from planner import build_plan
from executor import (ClusterMetadata, Executor, MigrationFailed, Phase,
                      StaleGenerationError)

SCHEMA = [("pid", ColType.STRING), ("nbytes", ColType.INT),
          ("load_factor", ColType.FLOAT)]


def banner(title: str) -> None:
    print(f"\n=== {title} ===")


def load_weights_from_batches():
    batch1 = TypedBatch.from_rows(SCHEMA, [
        ("p1", 500, 0.9), ("p2", 400, 0.7), ("p3", 350, NULL),
        ("p4", 300, 0.4), ("p5", 250, 0.3),
    ])
    batch2 = TypedBatch.from_rows(SCHEMA, [
        ("p6", 200, 0.2), ("p7", 150, NULL), ("p8", 100, 0.1),
        ("p9", 80, float("nan")), ("p10", 60, 0.1),
        ("p_huge", 5000, 1.0),
        ("p5", NULL, 0.3),          # NULL weight: skipped by the sum
    ])
    table = batch1.concat(batch2)
    weights = group_sum_int(table, "pid", "nbytes")
    print(f"merged batches: {batch1.nrows} + {batch2.nrows} = {table.nrows} rows")
    lf = sum_float(table, "load_factor")
    print(f"load_factor sum is NaN (NaN != NULL, NaN propagates): {is_nan(lf)}")
    print(f"exact integer weights: {weights}")
    return weights


def build_cluster(weights) -> Cluster:
    slots = {f"s{i}": Slot(f"s{i}", 1300) for i in range(4)}
    partitions = {}
    for pid, w in sorted(weights.items()):
        allowed = frozenset({"s0", "s1"}) if pid == "p9" else None
        partitions[pid] = Partition(pid, w, allowed)
    current = {
        "p1": "s0", "p2": "s0", "p3": "s0",
        "p4": "s1", "p5": "s1",
        "p6": "s2", "p7": "s2",
        "p8": "s3", "p9": "s3", "p10": "s3",
    }
    anti = frozenset({frozenset({"p1", "p2"})})
    return Cluster(slots=slots, partitions=partitions,
                   anti_affinity=anti, current=current)


def make_metadata(cluster) -> ClusterMetadata:
    placed = {pid: cluster.partitions[pid] for pid in cluster.current}
    sub = Cluster(slots=cluster.slots, partitions=placed,
                  anti_affinity=cluster.anti_affinity, current=cluster.current)
    return ClusterMetadata.from_initial(sub)


def show_loads(title, loads, cluster):
    print(title)
    for sid in sorted(loads):
        cap = cluster.slots[sid].capacity
        print(f"  {sid}: {loads[sid]:>5} / {cap} bytes")


def main() -> None:
    banner("1. typed batch input (NULL != NaN, exact ints)")
    weights = load_weights_from_batches()
    cluster = build_cluster(weights)

    banner("2. exact solve (min max-load, then min moved bytes)")
    result = solve(cluster)
    show_loads("before:", result.before_loads, cluster)
    show_loads("after:", result.loads, cluster)
    print(f"max load: {result.max_load}  proved lower bound: "
          f"{result.max_load_lower_bound}  gap: {result.gap}")
    print(f"moved bytes: {result.moved_bytes} (lower bound "
          f"{result.moved_bytes_lower_bound})  status: {result.status}")
    print(f"search nodes: {result.nodes}")
    if result.unmet:
        total = sum(result.unmet.values())
        print(f"UNMET demand (never split, fits no slot): {result.unmet} "
              f"total={total} bytes")
    check = verify_assignment(cluster, result.assignment)
    print(f"independent verification: ok={check['ok']} errors={check['errors']}")

    banner("3. deterministic plan under per-round budget")
    budget = 600
    plan = build_plan(cluster, result.assignment, budget, generation=7)
    print(f"plan_id={plan.plan_id} generation={plan.generation} "
          f"budget/round={budget}")
    for rnd in plan.rounds:
        desc = ", ".join(f"{m.pid}:{m.src}->{m.dst}({m.nbytes})"
                         for m in rnd.moves)
        print(f"  round {rnd.index}: {rnd.nbytes} bytes  [{desc}]")
    for m in plan.unschedulable:
        print(f"  UNSCHEDULABLE (>{budget} bytes): {m.pid} {m.nbytes} bytes")

    banner("4. phased execution (reserve -> copy -> switch -> release)")
    meta = make_metadata(cluster)
    ex = Executor(meta, budget)
    ex.execute(plan)
    show_loads("slot bytes after commit:",
               {sid: meta.data_bytes(sid) for sid in meta.capacities}, cluster)
    print(f"consistency: {ex.consistency_report()}")
    print(f"budget ledger after commit: {meta.budget_ledger}")

    banner("5. failure injection -> staged metadata rollback")
    meta2 = make_metadata(cluster)
    ex2 = Executor(meta2, budget)
    victim = plan.rounds[0].moves[0]
    try:
        ex2.execute(plan, fail_at=(victim.pid, Phase.COPY))
    except MigrationFailed as exc:
        print(f"caught: {exc}")
    print(f"ownership of {victim.pid} still: {meta2.ownership[victim.pid]}")
    print(f"reservations on target: "
          f"{dict(meta2.reservations[victim.dst])}")
    print(f"budget ledger after rollback: {meta2.budget_ledger}")
    print(f"consistency: {ex2.consistency_report()}")

    banner("6. generation fencing + idempotent replay")
    try:
        ex.handle_completion_ack("p1", generation=plan.generation - 1)
    except StaleGenerationError as exc:
        print(f"stale ack rejected: {exc}")
    again = ex.execute(plan)  # replay the very same plan
    skipped = sum(1 for _, o in again if o == "skipped-already-complete")
    print(f"replayed plan {plan.plan_id}: {skipped}/{len(again)} steps skipped")
    print(f"consistency after replay: {ex.consistency_report()}")
    print(f"reservations after replay: "
          f"{ {sid: dict(r) for sid, r in meta.reservations.items() if r} }")


if __name__ == "__main__":
    main()
