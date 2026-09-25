"""Demo run over the synthetic sample world (Python 3.11, stdlib only)."""
from __future__ import annotations

from proofsched import (
    Fact,
    GreedyScheduler,
    ProofEngine,
    exact_max_weight,
    replay_certificate,
)
from proofsched.model import GoalStatus
from proofsched.sample import build_sample


def show(title, engine):
    report = engine.status_report()
    parts = [f"{gid}={report[gid].value}" for gid in sorted(report)]
    print(f"  {title:<40} budget={engine.budget}  " + "  ".join(parts))


def main() -> None:
    facts, rules, goals = build_sample()
    total_weight = sum(g.weight for g in goals.values())
    print("=== synthetic sample world (all data fabricated) ===")
    print(f"  facts={sorted(facts)}")
    print(f"  rules={sorted(rules)}  goal weights={ {g: goals[g].weight for g in sorted(goals)} }")

    print("\n=== 1. budget 0: everything undecided ===")
    engine = ProofEngine(facts, rules, goals, budget=0)
    show("initial", engine)

    print("\n=== 2. exact reference vs greedy heuristic (proved weight) ===")
    print(f"  {'budget':>6}  {'exact':>6}  {'greedy':>6}")
    for budget in range(0, 8):
        exact = exact_max_weight(facts, rules, goals, budget)
        trial = ProofEngine(facts, rules, goals, budget=budget)
        GreedyScheduler(trial).run(exhaust=True)
        print(f"  {budget:>6}  {exact.weight:>6.1f}  {trial.proved_weight():>6.1f}")
    print("  (greedy is a heuristic: no optimality claimed; exact is the reference)")

    print("\n=== 3. full run, budget 7 (enough to exhaust the finite graph) ===")
    engine = ProofEngine(facts, rules, goals, budget=7)
    GreedyScheduler(engine).run(exhaust=True)
    show("after greedy + exhaust", engine)
    print(f"  proved weight {engine.proved_weight():.1f} / {total_weight:.1f}")
    stats = engine.stats
    print(f"  steps_computed={stats.steps_computed} "
          f"shared_work_avoided={engine.shared_work_avoided()} "
          f"duplicate_steps_avoided={stats.duplicate_steps_avoided}")

    print("\n=== 4. replayable evidence ===")
    cert = engine.certificate("g1")
    ok = replay_certificate(cert, engine.facts, engine.rules)
    tampered_facts = dict(engine.facts)
    tampered_facts["obs_power"] = Fact("obs_power", version=99)
    bad = replay_certificate(cert, tampered_facts, engine.rules)
    print(f"  g1 certificate replays against current snapshot: {ok}")
    print(f"  same certificate against tampered fact version:  {bad}")

    print("\n=== 5. retract obs_power: invalidation + budget recovery ===")
    engine.retract_fact("obs_power")
    show("after retract obs_power", engine)
    stats = engine.stats
    print(f"  budget_recovered={stats.budget_recovered} "
          f"frontier_revalidations={stats.frontier_revalidations} "
          f"alternative_proofs_reused={stats.alternative_proofs_reused}")
    print("  g1 stays proved: ticket_needed re-attached to the already-computed")
    print("  alternative proof (r5) for free; g2/g3 lose net_ok and, with the")
    print("  frontier exhausted, are disproved rather than merely unsearched.")

    print("\n=== 6. adding budget never revokes verified conclusions ===")
    engine2 = ProofEngine(facts, rules, goals, budget=4)
    GreedyScheduler(engine2).run()
    before = engine2.status_report()
    show("budget 4, greedy", engine2)
    engine2.add_budget(3)
    GreedyScheduler(engine2).run(exhaust=True)
    after = engine2.status_report()
    show("after add_budget(3) + rerun", engine2)
    kept = all(
        after[g] is GoalStatus.PROVED
        for g in before
        if before[g] is GoalStatus.PROVED
    )
    print(f"  previously proved goals still proved: {kept}")


if __name__ == "__main__":
    main()
