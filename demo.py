"""Synthetic-data demo of incremental distribution-distance monitoring.

Run with:  python3.11 demo.py

Everything below is generated synthetic data.  The reported distances are
descriptive statistics only -- they are NOT a formal privacy proof.
"""

import random
from fractions import Fraction

from distances import emd_general, emd_general_exhaustive, verify_emd_1d
from monitor import DistributionMonitor

SUPPORT = ["mild", "moderate", "severe", "critical"]
POSITIONS = [0, 1, 2, 3]


def fmt(value):
    if isinstance(value, Fraction):
        return f"{value} (~{float(value):.4f})"
    return str(value)


def show(evaluation):
    print(f"  class={evaluation.class_key!r} status={evaluation.status} "
          f"n_class={evaluation.n_class} n_baseline={evaluation.n_baseline} "
          f"baseline_version={evaluation.baseline_version}")
    if not evaluation.evaluated:
        return
    print(f"    TV  = {fmt(evaluation.tv)}")
    for bucket, contribution in evaluation.tv_bucket_contribs.items():
        print(f"      bucket {bucket!r:>10}: {fmt(contribution)}")
    print(f"    EMD ({evaluation.emd_mode}) = {fmt(evaluation.emd)}")
    for gap, contribution in evaluation.emd_contribs.items():
        print(f"      gap {gap!r:>22}: {fmt(contribution)}")
    if evaluation.tv_evidence:
        evidence = evaluation.tv_evidence
        print(f"    TV threshold {evidence['threshold']}: "
              f"exceeded={evidence['exceeded']}")
        for step in evidence["contributions"]:
            print(f"      {step['bucket']!r:>10}: +{fmt(step['contribution'])}"
                  f"  cumulative={fmt(step['cumulative'])}")


def main():
    rng = random.Random(20260927)

    print("=" * 72)
    print("1. Build synthetic population (ordered 1-D mode, TV threshold 1/5)")
    print("=" * 72)
    monitor = DistributionMonitor(
        SUPPORT, positions=POSITIONS, tv_threshold=Fraction(1, 5)
    )
    classes = {
        ("age:20-29", "region:north"): ["mild", "mild", "moderate"],
        ("age:30-39", "region:south"): ["moderate", "severe", "severe", "critical"],
        ("age:40-49", "region:east"): ["critical", "critical"],
    }
    record_id = 0
    for class_key, values in classes.items():
        for value in values:
            monitor.add_record(f"r{record_id}", class_key, value)
            record_id += 1
    print(f"baseline_version={monitor.baseline_version} "
          f"baseline_counts={dict(zip(SUPPORT, monitor.baseline_counts))}")
    for key in classes:
        show(monitor.evaluate(key))

    print()
    print("=" * 72)
    print("2. Withdrawals change the baseline -> every class must be re-evaluated")
    print("=" * 72)
    monitor.withdraw_record("r0")   # mild leaves class 1
    monitor.withdraw_record("r8")   # critical leaves class 3
    print(f"baseline_version={monitor.baseline_version} "
          f"stale_classes={monitor.stale_classes()}")
    for key, evaluation in monitor.evaluate_all().items():
        show(evaluation)
    print(f"stale after re-evaluation: {monitor.stale_classes()}")

    print()
    print("=" * 72)
    print("3. Empty equivalence class: skipped, never divides by zero")
    print("=" * 72)
    for rid in ("r6", "r7"):  # both remaining records of class 3
        monitor.withdraw_record(rid)
    show(monitor.evaluate(("age:40-49", "region:east")))

    print()
    print("=" * 72)
    print("4. Optimal 1-D transport flow + certificate verification")
    print("=" * 72)
    p_counts, q_counts = [3, 0, 1, 0], [0, 2, 0, 2]
    from distances import emd_1d
    result = emd_1d(p_counts, q_counts, POSITIONS)
    print(f"  P counts={p_counts}  Q counts={q_counts}")
    print(f"  W_1 = {fmt(result['distance'])}")
    print("  optimal flow (mass P_i -> Q_j):")
    for i, row in enumerate(result["flow"]):
        moves = {SUPPORT[j]: str(f) for j, f in enumerate(row) if f}
        if moves:
            print(f"    from {SUPPORT[i]!r:>10}: {moves}")
    verify_emd_1d(p_counts, q_counts, POSITIONS,
                  result["flow"], result["distance"], result["dual"])
    print("  verifier: feasibility + cost + dual certificate all OK")

    print()
    print("=" * 72)
    print("5. General cost matrix: genuine min-cost flow, exhaustive cross-check")
    print("=" * 72)
    # Non-metric ground cost (squared distances): the 1-D cumulative formula
    # would be invalid here.
    squared = [[0, 1, 4, 9], [1, 0, 1, 4], [4, 1, 0, 1], [9, 4, 1, 0]]
    distance, flow, contribs = emd_general(p_counts, q_counts, squared)
    brute = emd_general_exhaustive(p_counts, q_counts, squared)
    line = emd_1d(p_counts, q_counts, POSITIONS)["distance"]
    print(f"  cost matrix = squared distances {squared[0]} ...")
    print(f"  min-cost-flow distance = {fmt(distance)}")
    print(f"  exhaustive optimum     = {fmt(brute)}  (match: {distance == brute})")
    print(f"  naive 1-D cumulative   = {fmt(line)}  (INVALID for this cost)")
    print(f"  per-source-bucket cost contributions: "
          f"{dict(zip(SUPPORT, map(str, contribs)))}")

    print()
    print("=" * 72)
    print("6. Randomised synthetic cross-check (min-cost flow vs exhaustive)")
    print("=" * 72)
    checked = 0
    for _ in range(30):
        k = rng.choice((2, 3))
        p = [rng.randrange(0, 4) for _ in range(k)]
        q = [rng.randrange(0, 4) for _ in range(k)]
        if sum(p) == 0 or sum(q) == 0:
            continue
        cost = [[rng.randrange(0, 6) for _ in range(k)] for _ in range(k)]
        d_fast, _, _ = emd_general(p, q, cost)
        d_slow = emd_general_exhaustive(p, q, cost)
        assert d_fast == d_slow, (p, q, cost, d_fast, d_slow)
        checked += 1
    print(f"  {checked} random tiny instances: min-cost flow == exhaustive optimum")


if __name__ == "__main__":
    main()
