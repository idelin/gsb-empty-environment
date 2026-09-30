"""End-to-end demo: seeded Laplace noise on public fixed buckets, exact
rational QP projection with overlapping consistency constraints, and
independent KKT verification. Synthetic counts only.

Run:  python3 examples/run_example.py
"""
import os
import sys
from fractions import Fraction

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from dpproj import (ConsistencyConstraint, NoiseLedger, NoisedTarget,
                    PublicTarget, ReleaseConfig, run_release,
                    verify_release)
from dpproj.qp import InfeasibleError, verify_kkt

SEED = 20240601
COUNTS = {"north": 12, "south": 8, "east": 15, "west": 5}  # synthetic


def show(title, res):
    print(f"  status={res.status}  converged={res.converged}  "
          f"iterations={res.iterations}")
    print("  noisy (pre-projection, float):")
    for k, v in res.noisy_counts.items():
        print(f"    {k:>6}: {v:+.4f}")
    print("  projected (post-processing, exact rational):")
    for k, v in res.projected.items():
        print(f"    {k:>6}: {float(v):.4f}   (= {v})")
    print(f"  total target: {res.total_target}  "
          f"sum(projected): {sum(res.projected.values())}")


def main():
    print("=" * 72)
    print("1) Public total + overlapping group consistency constraints")
    print("=" * 72)
    cfg = ReleaseConfig(
        release_id="regions-2024H1",
        buckets=tuple(COUNTS),
        epsilon=Fraction(1),              # bucket-noise epsilon
        l1_sensitivity=Fraction(2),       # bounded-contribution L1
        total=PublicTarget(Fraction(40)),  # total is public knowledge
        constraints=(
            ConsistencyConstraint(
                coeffs={"north": 1, "south": 1},
                target=PublicTarget(Fraction(20)), name="north+south"),
            ConsistencyConstraint(
                coeffs={"south": 1, "east": 1},
                target=PublicTarget(Fraction(23)), name="south+east"),
        ),
    )
    ledger = NoiseLedger(SEED)
    res = run_release(cfg, ledger, COUNTS)
    show("public total", res)
    print(f"  independent KKT verification: {verify_release(cfg, res)}")

    print()
    print("  Repeat the same release with the same ledger "
          "(noise is reused, never re-drawn):")
    res_repeat = run_release(cfg, ledger, COUNTS)
    print(f"  identical noisy counts: "
          f"{res_repeat.noisy_counts == res.noisy_counts}")
    print(f"  identical projection:   "
          f"{res_repeat.projected == res.projected}")
    print(f"  Recompute from scratch with the same seed:")
    res_fresh = run_release(cfg, NoiseLedger(SEED), COUNTS)
    print(f"  identical noisy counts: "
          f"{res_fresh.noisy_counts == res.noisy_counts}")

    print()
    print("=" * 72)
    print("2) Total from a separately noised source (own epsilon share)")
    print("=" * 72)
    cfg2 = ReleaseConfig(
        release_id="regions-noised-total",
        buckets=tuple(COUNTS),
        epsilon=Fraction(1),
        l1_sensitivity=Fraction(2),
        total=NoisedTarget(epsilon=Fraction(1, 2)),
    )
    ledger2 = NoiseLedger(SEED)
    res2 = run_release(cfg2, ledger2, COUNTS)
    show("noised total", res2)
    print(f"  ledger keys: {sorted(ledger2.draws)}")

    print()
    print("=" * 72)
    print("3) Infeasible total is explicitly rejected")
    print("=" * 72)
    cfg3 = ReleaseConfig(
        release_id="bad-total",
        buckets=tuple(COUNTS),
        epsilon=Fraction(1),
        l1_sensitivity=Fraction(2),
        total=PublicTarget(Fraction(-5)),   # impossible with x >= 0
    )
    try:
        run_release(cfg3, NoiseLedger(SEED), COUNTS)
        print("  BUG: release was not rejected")
    except InfeasibleError as exc:
        print(f"  rejected with InfeasibleError: {exc}")

    print()
    print("=" * 72)
    print("4) Iteration budget exhausted -> marked not converged")
    print("=" * 72)
    cfg4 = ReleaseConfig(
        release_id="tight-budget",
        buckets=tuple(COUNTS),
        epsilon=Fraction(1),
        l1_sensitivity=Fraction(2),
        total=PublicTarget(Fraction(40)),
        max_iterations=1,
    )
    res4 = run_release(cfg4, NoiseLedger(SEED), COUNTS)
    print(f"  status={res4.status}  converged={res4.converged}  "
          f"certificate={'none' if res4.kkt is None else 'present'}")


if __name__ == "__main__":
    main()
