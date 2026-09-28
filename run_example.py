"""Runnable demo: exact power reference + honestly-labelled simulation.

Usage: python3.11 run_example.py
"""
from __future__ import annotations

import random

from binomial_power import (
    exact_power,
    min_sample_size,
    plan_looks,
    scenario_streams,
    simulate_rejection_rate,
)

BASE_SEED = 20260929


def show_result(res) -> None:
    print(f"  scenario={res.scenario!r} seed={res.seed} "
          f"n_per_trial={res.n_per_trial} critical={res.critical}")
    print(f"    [EXACT]     power reference      = {res.exact_power:.6f}")
    print(f"    [EMPIRICAL] rejections/trials    = "
          f"{res.rejections}/{res.trials_done} = {res.estimate:.6f}")
    print(f"    [EMPIRICAL] {res.ci_confidence:.1%} Clopper-Pearson CI = "
          f"[{res.ci[0]:.6f}, {res.ci[1]:.6f}] "
          f"(half-width {0.5 * (res.ci[1] - res.ci[0]):.6f})")
    print(f"    status={res.status} after {res.looks_taken} look(s): "
          f"{res.stop_reason}")
    if res.pending_half_width is not None:
        print(f"    [PENDING] budget interrupted; CI still "
              f"{res.pending_half_width:.6f} wider than target -- "
              f"undecided range kept, not declared converged")


def main() -> None:
    p0, alpha = 0.5, 0.05

    print("== 1. Exact power reference (binomial tail recursion) ==")
    n, p1 = 100, 0.6
    critical, size, power = exact_power(n, p0, p1, alpha)
    print(f"  n={n}, H0: p<={p0}, H1: p={p1}, alpha={alpha}")
    print(f"  fixed rule: reject iff X >= {critical} "
          f"(actual size {size:.4f})")
    print(f"  [EXACT] power = {power:.6f}")

    print("\n== 2. Simulation with pre-declared error spending ==")
    names = ["p1=0.55", "p1=0.60", "p1=0.65"]
    alts = {"p1=0.55": 0.55, "p1=0.60": 0.60, "p1=0.65": 0.65}
    streams = scenario_streams(BASE_SEED, names)
    plan = plan_looks(max_trials=20_000, n_looks=4, total_alpha=0.05, rho=2.0)
    print(f"  base_seed={BASE_SEED}; look plan: trials={plan.looks}, "
          f"per-look error={[round(e, 5) for e in plan.errors]}")
    for name in names:
        child_seed, rng = streams[name]
        res = simulate_rejection_rate(
            n, p0, alts[name], alpha,
            rng=rng, max_trials=20_000, target_half_width=0.01,
            look_plan=plan, seed=child_seed, scenario=name,
        )
        show_result(res)

    print("\n== 3. Reproducibility: same base seed, fresh streams ==")
    replay = scenario_streams(BASE_SEED, names)
    child_seed, rng = replay["p1=0.60"]
    res2 = simulate_rejection_rate(
        n, p0, 0.60, alpha,
        rng=rng, max_trials=20_000, target_half_width=0.01,
        look_plan=plan, seed=child_seed, scenario="p1=0.60",
    )
    first = simulate_rejection_rate(
        n, p0, 0.60, alpha,
        rng=random.Random(streams["p1=0.60"][0]),
        max_trials=20_000, target_half_width=0.01,
        look_plan=plan, seed=streams["p1=0.60"][0], scenario="p1=0.60",
    )
    print(f"  run A rejections={first.rejections}, run B rejections="
          f"{res2.rejections} -> identical: {first.rejections == res2.rejections}")

    print("\n== 4. Budget interruption keeps the pending range ==")
    child_seed, rng = streams["p1=0.55"]
    tight = simulate_rejection_rate(
        n, p0, 0.55, alpha,
        rng=random.Random(child_seed), max_trials=200,
        target_half_width=1e-4, seed=child_seed, scenario="p1=0.55/tiny-budget",
    )
    show_result(tight)

    print("\n== 5. Minimum n for target power (adjacent-point evidence) ==")
    ans = min_sample_size(p0, 0.6, alpha=alpha, target_power=0.8)
    print(f"  n_min={ans.n_min} (critical={ans.critical}, "
          f"actual size={ans.actual_size:.4f})")
    print(f"  {ans.evidence}")


if __name__ == "__main__":
    main()
