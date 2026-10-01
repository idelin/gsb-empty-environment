#!/usr/bin/env python3.11
"""End-to-end demo: subset-minimal core vs minimum-cardinality core.

Run:  python3.11 run_demo.py
"""

from __future__ import annotations

from minicore import (
    CachedSolver,
    Status,
    deletion_based_core,
    enumerate_all_subsets,
    minimum_cardinality_core,
    verify_core,
)
from minicore.demo import order_sensitive_problem


def show_records(title, records, problem):
    print(f"  {title}")
    for rec in records:
        names = [problem.constraints[i].name.split(":")[0] for i in rec.tested]
        tested = "{" + ",".join(names) + "}" if names else "{}"
        note = f"  -- {rec.note}" if rec.note else ""
        print(
            f"    #{rec.step:02d} [{rec.phase:<12}] tested={tested:<14} "
            f"-> {rec.status.value:<7} nodes={rec.nodes:<4} {rec.decision}{note}"
        )


def main() -> None:
    problem = order_sensitive_problem()
    names = [c.name for c in problem.constraints]
    print("=" * 78)
    print("Constraint pool (finite domains a,b in {0,1}):")
    for i, name in enumerate(names):
        print(f"  [{i}] {name}")
    print("MUSes: {k1,k3} {k2,k4} (size 2); {k1,k2,k5} {k3,k4,k5} (size 3)")

    print("=" * 78)
    print("1) Deletion-based subset-minimal core (pool order)")
    solver = CachedSolver(problem, per_call_budget=5_000)
    result = deletion_based_core(problem, solver)
    core_names = [names[i] for i in result.core]
    print(f"  kind={result.kind}  core={core_names}  undecided={result.undecided}")
    show_records("check records:", result.records, problem)
    print(
        f"  cache: hits={solver.hits} misses={solver.misses} "
        f"solver_calls={solver.solver_calls} version={solver.version}"
    )

    print("=" * 78)
    print("2) Minimum-cardinality core via hitting-set search (lower bounds)")
    solver2 = CachedSolver(problem, per_call_budget=5_000)
    min_result = minimum_cardinality_core(problem, solver2)
    min_names = [names[i] for i in min_result.core]
    print(
        f"  kind={min_result.kind}  core={min_names}  "
        f"certified_lower_bound={min_result.lower_bound}"
    )
    show_records("hitting-set trace:", min_result.records, problem)
    print(
        f"  => subset-minimal core has size {len(result.core)}, "
        f"minimum-cardinality core has size {len(min_result.core)}: "
        "subset-minimal != minimum-cardinality"
    )

    print("=" * 78)
    print("3) Independent verification by exhaustive subset enumeration")
    table = enumerate_all_subsets(problem)
    for label, core in (("deletion core", result.core), ("min-card core", min_result.core)):
        report = verify_core(problem, core, table=table)
        print(f"  {label} {report.core}:")
        print(f"    core status          : {report.core_status.value}")
        print(f"    deletion witnesses   : "
              f"{ {i: s.value for i, s in report.deletion_witnesses.items()} }")
        print(f"    subset-minimal       : {report.is_subset_minimal}")
        print(f"    minimum-cardinality  : {report.is_minimum_cardinality} "
              f"(min size = {report.min_cardinality})")
        print(f"    all MUSes            : {report.all_muses}")
        print(f"    recovery consistent  : {report.recovery_consistent} "
              f"(is the complement satisfiable?)")
        print(f"    unknown subsets      : {report.unknown_subsets}")

    print("=" * 78)
    print("4) Budget exhaustion propagates as UNDECIDED (never SAT/UNSAT)")
    # The initial UNSAT proof of the full pool costs exactly 2 nodes; allow
    # exactly that, so every deletion check afterwards starves -> UNKNOWN.
    starved = CachedSolver(problem, per_call_budget=6)
    poor = deletion_based_core(problem, starved, per_call_budget=6, total_budget=2)
    print(f"  kind={poor.kind}  undecided={poor.undecided}  "
          f"candidate_core_size={len(poor.core)}")
    show_records("starved-budget records:", poor.records, problem)
    assert poor.undecided, "budget exhaustion must propagate as undecided"
    assert poor.kind != "subset_minimal", "minimality must not be claimed"
    print("  OK: no item was removed on UNKNOWN; no minimality was claimed.")
    print("=" * 78)


if __name__ == "__main__":
    main()
