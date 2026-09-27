"""End-to-end demo: exact references + labeled empirical estimates.

Run from the repo root:  python3.11 examples/run_examples.py
"""

from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from binpower import (  # noqa: E402
    Scenario,
    adaptive_allocate,
    exact_power,
    group_sequential_design,
    min_sample_size,
    obrien_fleming_spending,
    run_scenarios,
    simulate_group_sequential,
    spawn_streams,
)

MASTER_SEED = 20260927


def line() -> None:
    print("-" * 78)


def demo_exact_reference() -> None:
    print("1) EXACT power reference (tail-probability recursion, kind=exact)")
    ref = exact_power(n=158, p0=0.5, p1=0.6, alpha=0.05)
    print(f"   fixed rule: reject H0: p<=0.5 iff X >= {ref.critical} out of n={ref.n}")
    print(f"   [exact] size = {ref.size:.6f} (<= alpha=0.05), "
          f"power at p1=0.6 = {ref.power:.6f}")
    line()


def demo_simulation() -> None:
    print("2) EMPIRICAL rejection rates (kind=empirical) with Clopper-Pearson CIs")
    scenarios = [
        Scenario("null p=0.50", n=158, p0=0.5, p=0.50),
        Scenario("alt  p=0.60", n=158, p0=0.5, p=0.60),
        Scenario("alt  p=0.65", n=158, p0=0.5, p=0.65),
    ]
    results = run_scenarios(scenarios, master_seed=MASTER_SEED, trials=4000)
    for name, est in results.items():
        print(f"   {name}: rate={est.rate:.4f} "
              f"CI95=[{est.ci_low:.4f}, {est.ci_high:.4f}] "
              f"(seed={est.seed}, trials={est.trials}) "
              f"[exact ref={est.exact_power:.4f}]")
    print("   -> the point rate is an ESTIMATE; the interval, not the point,")
    print("      is comparable to the exact reference.")
    line()


def demo_min_sample_size() -> None:
    print("3) Minimum n for target power 0.80 (EXACT scan, adjacent evidence)")
    res = min_sample_size(p0=0.5, p1=0.6, alpha=0.05, target_power=0.8)
    print(f"   [exact] n={res.n}: c={res.critical}, size={res.size:.4f}, "
          f"power={res.power:.6f} >= 0.80")
    print(f"   [exact] n={res.n_below}: c={res.critical_below}, "
          f"power={res.power_below:.6f} <  0.80   (adjacent-point evidence)")
    line()


def demo_group_sequential() -> None:
    print("4) Group-sequential design, pre-declared O'Brien-Fleming spending")
    design = group_sequential_design(
        n_looks=[53, 106, 158], p0=0.5, alpha=0.05,
        spending=obrien_fleming_spending(0.05))
    for b in design.looks:
        crit = f"S>={b.critical}" if b.critical <= b.n else "unreachable"
        print(f"   look {b.look} (n={b.n:3d}): reject if {crit:9s} "
              f"budget={b.alpha_budget_cum:.5f} spent={b.alpha_spent_cum:.5f}")
    print(f"   [exact] total size = {design.size:.5f} <= 0.05")
    stream = spawn_streams(MASTER_SEED, ["gs-demo"])["gs-demo"]
    est = simulate_group_sequential(design, p=0.6, trials=4000, stream=stream)
    print(f"   [empirical] rejection rate at p=0.6: {est.rate:.4f} "
          f"CI95=[{est.ci_low:.4f}, {est.ci_high:.4f}] "
          f"(seed={est.seed}, trials={est.trials}) "
          f"[exact power={est.exact_power:.4f}]")
    print("   stopping conditions observed:")
    for cond, count in est.stopping_conditions:
        print(f"     {count:5d}x {cond}")
    line()


def demo_adaptive() -> None:
    print("5) Adaptive budget allocation (test rule stays fixed)")
    scenarios = [
        Scenario("null", n=158, p0=0.5, p=0.50),
        Scenario("alt", n=158, p0=0.5, p=0.60),
    ]
    conv = adaptive_allocate(scenarios, master_seed=MASTER_SEED,
                             total_budget=30000, initial_trials=200,
                             batch_trials=500, tolerance=0.05)
    print(f"   ample budget -> status={conv.status}, spent={conv.spent}")
    for name, est in conv.estimates.items():
        print(f"     {name}: rate={est.rate:.4f} "
              f"CI95=[{est.ci_low:.4f}, {est.ci_high:.4f}] "
              f"trials={est.trials} [exact={est.exact_power:.4f}]")
    tight = adaptive_allocate(scenarios, master_seed=MASTER_SEED,
                              total_budget=600, initial_trials=200,
                              batch_trials=200, tolerance=0.02)
    print(f"   tight budget -> status={tight.status}, spent={tight.spent}, "
          f"pending={list(tight.pending)}")
    for name in tight.pending:
        est = tight.estimates[name]
        print(f"     {name}: UNDECIDED range CI95=[{est.ci_low:.4f}, "
              f"{est.ci_high:.4f}] after {est.trials} trials "
              f"(budget interrupted; range preserved, not collapsed)")
    line()


if __name__ == "__main__":
    print(f"master seed: {MASTER_SEED} (every scenario stream is derived "
          f"from it and reproducible)")
    line()
    demo_exact_reference()
    demo_simulation()
    demo_min_sample_size()
    demo_group_sequential()
    demo_adaptive()
