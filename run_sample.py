"""End-to-end sample on synthetic data (deterministic, seedable).

Run:  python3 run_sample.py
"""
from __future__ import annotations

import json
import random
from fractions import Fraction

from proofpack import (
    FilterRule, Record, aggregate, build_package, retract_contribution,
    verify_package,
)


def synthetic_records(count: int = 40, seed: int = 7) -> list[Record]:
    rng = random.Random(seed)
    keys = sorted(rng.randint(1, 100) for _ in range(count))
    return [
        Record(rid=f"F{i:04d}", fact_version=1, key=k,
               value=rng.randint(1, 50), weight=rng.randint(0, 5))
        for i, k in enumerate(keys)
    ]


def mean_of_batch_means(records: list[Record], batches: int = 4) -> Fraction:
    """The WRONG way to pool: unweighted average of per-batch weighted means."""
    chunks = [records[i::batches] for i in range(batches)]
    means = []
    for c in chunks:
        den = sum(r.weight for r in c)
        if den:
            means.append(Fraction(sum(r.value * r.weight for r in c), den))
    return sum(means, Fraction(0)) / len(means)


def show_report(title: str, report: dict) -> None:
    print(f"\n== {title} ==")
    for c in report["checks"]:
        mark = "ok " if c["ok"] else "FAIL"
        extra = f"  ({c['detail']})" if c["detail"] else ""
        print(f"  [{mark}] {c['check']}{extra}")
    print(f"  => overall: {'PASS' if report['ok'] else 'FAIL'}")


def main() -> None:
    records = synthetic_records()
    rule = FilterRule(rule_id="rule:key-range", version=1,
                      field="key", lo=20, hi=60)

    pkg = build_package(records, rule, package_id="pkg:demo", version=1)
    print("dataset root :", pkg["dataset"]["root"])
    print("rule         :", json.dumps(pkg["rule"], sort_keys=True))
    print("contributions:", len(pkg["evidence"]["contributions"]),
          "of", pkg["dataset"]["leaf_count"], "records")
    print("aggregates   :", json.dumps(pkg["aggregates"], sort_keys=True))
    print("summary      :", pkg["contribution_summary"]["digest"][:16], "...")
    print("package digest:", pkg["package_digest"][:16], "...")

    selected = [r for r in records if rule.matches(r)]
    trap = mean_of_batch_means(selected)
    print("\nweighted mean (correct)      :", pkg["aggregates"]["wavg"])
    print("mean of batch means (WRONG)  :", trap,
          "  <- rejected: not the weighted mean")

    show_report("verify v1 (fresh package)",
                verify_package(pkg, expected_root=pkg["dataset"]["root"]))

    # --- incremental retraction -------------------------------------------
    victim = pkg["evidence"]["contributions"][3]["record"]
    pkg2 = retract_contribution(pkg, victim["rid"])
    print(f"\nretracted {victim['rid']} "
          f"(value={victim['value']}, weight={victim['weight']})")
    for f in ("sum", "count", "wavg_numerator", "wavg_denominator", "wavg"):
        print(f"  {f}: {pkg['aggregates'][f]}  ->  {pkg2['aggregates'][f]}")
    print("  summary digest changed:",
          pkg["contribution_summary"]["digest"] != pkg2["contribution_summary"]["digest"])
    show_report("verify v2 (after retraction, cross-checked vs v1)",
                verify_package(pkg2, expected_root=pkg["dataset"]["root"],
                               previous_package=pkg))

    # --- zero total weight -------------------------------------------------
    zw = [Record(rid=f"Z{i}", fact_version=1, key=k, value=v, weight=w)
          for i, (k, v, w) in enumerate(
              [(5, 10, 1), (10, 7, 0), (15, 9, 0), (20, 4, 0), (25, 6, 3)])]
    zrule = FilterRule(rule_id="rule:key-range", version=1,
                       field="key", lo=10, hi=20)
    zpkg = build_package(zw, zrule, package_id="pkg:zero-weight", version=1)
    print("\nzero-denominator package:")
    print("  aggregates:", json.dumps(zpkg["aggregates"], sort_keys=True))
    show_report("verify zero-denominator package", verify_package(zpkg))


if __name__ == "__main__":
    main()
