"""Runnable end-to-end example (synthetic data only).

    python3.11 demo.py

Run it twice: the second run reuses the persisted noise ledger
(demo_ledger.json) instead of re-drawing noise.
"""
from fractions import Fraction
from pathlib import Path

from noise import NoiseLedger
from pipeline import (BucketSchema, publish, reconcile, simple_release,
                      synthetic_counts)
from qp import solve_by_enumeration
from pipeline import _publications

LEDGER_PATH = Path("demo_ledger.json")


def show(title, mapping):
    print(f"  {title}")
    for k, v in mapping.items():
        print(f"    {k:>10}: {v}")


def main():
    print("=" * 64)
    print("1) Single-level release: noisy counts -> simplex projection")
    counts = synthetic_counts(num_users=60, n_buckets=5,
                              max_contribution=2, seed=101)
    ledger = NoiseLedger(master_seed=20261001, path=LEDGER_PATH)
    res = simple_release(counts, epsilon=Fraction(1), ledger=ledger,
                         max_contribution=2, total_source="public")
    print(f"  true counts         : {counts}")
    print(f"  raw noisy (separate): {[res['raw_noisy'][f'leaf:b{i}'] for i in range(5)]}")
    print(f"  total target        : {res['total_target']} (source: {res['total_source']})")
    print(f"  status              : {res['status']}")
    print(f"  postprocessed       : {[str(v) for v in res['postprocessed']]}")
    print(f"  sum(postprocessed)  : {sum(res['postprocessed'])}")

    print("=" * 64)
    print("2) Overlapping groups: weighted QP + KKT certificate")
    schema = BucketSchema(
        leaf_names=("a", "b", "c", "d"),
        groups=(("left", (0, 1)), ("mid", (1, 2)), ("right", (2, 3))),
    )
    counts2 = synthetic_counts(num_users=40, n_buckets=4,
                               max_contribution=2, seed=202)
    release = publish(schema, counts2, epsilon=Fraction(1), ledger=ledger,
                      max_contribution=2, total_source="noised")
    res2 = reconcile(release)
    show("raw noisy measurements:",
         {k: m["noisy"] for k, m in release.measurements.items()})
    print(f"  true counts        : {counts2}")
    print(f"  total target       : {release.total_value} (source: noised)")
    print(f"  status             : {res2['status']}  (iterations: {res2['iterations']})")
    print(f"  postprocessed      : {[str(v) for v in res2['postprocessed']]}")
    print(f"  kkt violations     : {res2['kkt_violations']}")
    cert = res2["certificate"]
    print(f"  certificate lambda : {cert['lambda']}")
    print(f"  certificate mu     : {cert['mu']}")

    ref = solve_by_enumeration(_publications(release), schema.n,
                               release.total_value)
    match = [Fraction(s) for s in cert["x"]] == ref.x
    print(f"  active-set enumeration reference matches: {match}")

    print("=" * 64)
    print("3) Failure modes")
    bad = publish(schema, counts2, Fraction(1),
                  NoiseLedger(master_seed=1), max_contribution=2,
                  total_source="noised")
    bad.total_value = -3
    r = reconcile(bad)
    print(f"  negative total -> status={r['status']} reason={r['reason']}")
    r2 = reconcile(release, max_iterations=0)
    print(f"  zero iteration budget -> status={r2['status']}")
    reused = sum(1 for m in release.measurements.values() if m["reused"])
    print(f"  ledger entries reused this run: {reused} "
          f"(>0 means noise was NOT re-drawn)")


if __name__ == "__main__":
    main()
