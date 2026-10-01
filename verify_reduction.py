"""Reference check for the symmetry-reduction strategy.

For small participant counts the full interleaving (no reduction) is
feasible, so we can validate the reduction: for every configuration the
reduced search must give the same verdict as the full-interleaving
reference, and counterexamples must have the same (minimal) length.
"""
from __future__ import annotations

from barrier_model import BarrierModel
from model_check import bfs_safety

CONFIGS = [
    ("correct", dict()),
    ("stale_arrival", dict(bug_stale_arrival=True)),
    ("stale_release", dict(bug_stale_release=True)),
    ("commit_no_reset", dict(bug_commit_no_reset=True)),
    ("no_commit", dict(bug_no_commit=True)),
]


def check_configs(ns=(2, 3), verbose=True):
    rows = []
    mismatches = []
    for n in ns:
        for name, kw in CONFIGS:
            model = BarrierModel(n=n, **kw)
            full = bfs_safety(model, use_symmetry=False)
            reduced = bfs_safety(model, use_symmetry=True)
            ok = full.verdict == reduced.verdict
            ce_note = "-"
            if full.verdict == "FAIL" and reduced.verdict == "FAIL":
                ok = ok and len(full.counterexample) == len(reduced.counterexample)
                ce_note = f"ce={len(full.counterexample)}/{len(reduced.counterexample)}"
            if not ok:
                mismatches.append((n, name, full.verdict, reduced.verdict))
            rows.append((n, name, full, reduced, ok, ce_note))
            if verbose:
                print(f"n={n} {name:<14} full={full.verdict:<4}({full.states:>6} st) "
                      f"reduced={reduced.verdict:<4}({reduced.states:>6} st) "
                      f"{ce_note:<10} {'OK' if ok else 'MISMATCH'}")
    return mismatches


if __name__ == "__main__":
    bad = check_configs()
    if bad:
        print(f"\nREDUCTION REFERENCE CHECK FAILED: {bad}")
        raise SystemExit(1)
    print("\nreduction reference check: all verdicts match the full interleaving")
