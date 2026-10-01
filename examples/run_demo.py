"""End-to-end demo: staged estimation, adaptive multi-estimation, and
exact Bernoulli coverage verification.  Run from the repo root:

    python3 examples/run_demo.py
"""

import pathlib
import random
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from bounded_estimation import (  # noqa: E402
    AdaptiveMultiEstimator,
    VariableSpec,
    estimate_mean,
    hoeffding_bernoulli_radius,
    verify_on_grid,
)


def banner(title):
    print("\n" + "=" * 68)
    print(title)
    print("=" * 68)


def show_result(result, truth=None):
    print(f"  status          : {result.status.value}")
    print(f"  converged       : {result.converged} (target met: {result.target_met})")
    print(f"  estimate        : {result.estimate:.6f}  [{result.estimate_kind.value}]")
    print(f"  radius          : {result.radius:.6f}  (guarantee: {result.guarantee_kind.value})")
    print(f"  epsilon target  : {result.epsilon}")
    print(f"  samples used    : {result.n}")
    print(f"  seed            : {result.seed}")
    if truth is not None:
        print(f"  true mean       : {truth}  (covered: {abs(result.estimate - truth) <= result.radius})")


def main():
    banner("1. Single bounded variable: Bernoulli(0.3), staged checking")
    rng = random.Random(20261001)
    result = estimate_mean(
        sampler=lambda r: 1.0 if r.random() < 0.3 else 0.0,
        low=0.0, high=1.0, epsilon=0.02, delta=0.05,
        max_samples=200_000, rng=rng, seed=20261001,
    )
    show_result(result, truth=0.3)
    print("  stage evidence (all checks kept):")
    for rec in result.evidence:
        print(f"    stage={rec.stage} n={rec.n:>7} mean={rec.mean:.4f} "
              f"radius={rec.radius:.4f} met={rec.target_met}")

    banner("2. Constant variable: fast convergence via exact degenerate bound")
    result = estimate_mean(
        sampler=lambda r: 0.7, low=0.0, high=1.0,
        epsilon=0.01, delta=0.05, max_samples=1_000_000, seed=7,
    )
    show_result(result, truth=0.7)

    banner("3. Budget exhausted: error target NOT met, honestly reported")
    result = estimate_mean(
        sampler=lambda r: 1.0 if r.random() < 0.5 else 0.0,
        low=0.0, high=1.0, epsilon=1e-4, delta=0.05,
        max_samples=256, seed=99,
    )
    show_result(result)
    print(f"  evidence records saved: {len(result.evidence)}")

    banner("4. Adaptive multi-variable estimation (pre-declared error split)")
    variables = [
        VariableSpec(name="rare_event", low=0.0, high=1.0, epsilon=0.03,
                     sampler=lambda r: 1.0 if r.random() < 0.15 else 0.0,
                     weight=1.0),
        VariableSpec(name="uniform", low=0.0, high=1.0, epsilon=0.03,
                     sampler=lambda r: r.random(), weight=1.0),
        VariableSpec(name="constant", low=0.0, high=1.0, epsilon=0.01,
                     sampler=lambda r: 0.25, weight=1.0),
    ]
    multi = AdaptiveMultiEstimator(
        variables=variables, delta=0.05, max_samples=2_000_000, seed=2026,
    ).run()
    print(f"  overall status: {multi.status.value}, converged: {multi.converged}")
    print(f"  total draws: {multi.total_draws}, seed: {multi.seed}")
    for name, res in multi.results.items():
        print(f"    {name:<10} est={res.estimate:.4f} radius={res.radius:.4f} "
              f"(eps={res.epsilon}) n={res.n} delta_i={res.delta:.4f} "
              f"met={res.target_met}")
    print("  adaptive selection log (first 6 steps):")
    for rec in multi.selection_log[:6]:
        print(f"    step={rec.step} -> {rec.variable:<10} draws={rec.draws:<6} ({rec.reason})")

    banner("5. Correlated shared samples (union bound, NO independence assumed)")
    def joint(r):
        u = r.random()
        return {"up": u, "down": 1.0 - u}  # perfectly anti-correlated

    corr = AdaptiveMultiEstimator(
        variables=[
            VariableSpec(name="up", low=0.0, high=1.0, epsilon=0.03),
            VariableSpec(name="down", low=0.0, high=1.0, epsilon=0.03),
        ],
        delta=0.05, max_samples=1_000_000, seed=555, joint_sampler=joint,
    ).run()
    print(f"  converged: {corr.converged}, draws: {corr.total_draws}")
    for name, res in corr.results.items():
        print(f"    {name:<5} est={res.estimate:.4f} radius={res.radius:.4f} n={res.n}")

    banner("6. Bernoulli small-domain EXACT coverage verification (recursion)")
    delta = 0.05
    radius_fn = hoeffding_bernoulli_radius(delta)
    for n in (10, 30, 60):
        check = verify_on_grid(n, radius_fn, target=1.0 - delta, grid_size=201)
        print(f"  n={n:>3}: worst coverage={check.worst_coverage:.4f} "
              f"at p={check.worst_p:.3f}  target={check.target:.2f}  "
              f"passed={check.passed}  [{check.kind.value}]")

    banner("7. EMPIRICAL coverage cross-check (simulation, labeled empirical)")
    trials, failures = 300, 0
    for t in range(trials):
        res = estimate_mean(
            sampler=lambda r: 1.0 if r.random() < 0.5 else 0.0,
            low=0.0, high=1.0, epsilon=0.1, delta=0.2,
            max_samples=50_000, seed=30_000 + t,
        )
        if abs(res.estimate - 0.5) > res.radius:
            failures += 1
    print(f"  trials={trials}, failures={failures}, "
          f"empirical failure rate={failures / trials:.4f} (delta=0.2)  [empirical]")


if __name__ == "__main__":
    main()
