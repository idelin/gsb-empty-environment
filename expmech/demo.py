"""Runnable synthetic-data demo:  python3.11 -m expmech.demo

All data below is synthetic. The frequency check at the end is a statistical
sanity experiment, NOT a formal privacy proof.
"""

from __future__ import annotations

from collections import Counter

from .mechanism import ExponentialMechanism
from .model import (
    Candidate,
    Constraints,
    utility_sensitivity_fixed_size,
)


def build_demo_mechanism() -> ExponentialMechanism:
    candidates = [
        Candidate("a1", 7, "A"),
        Candidate("a2", 3, "A"),
        Candidate("a3", -2, "A"),
        Candidate("b1", 5, "B"),
        Candidate("b2", 5, "B"),
        Candidate("c1", 9, "C"),
        Candidate("c2", 0, "C"),
        Candidate("c3", 4, "C"),
    ]
    constraints = Constraints(
        subset_size=3,
        group_quotas={"A": 2, "B": 1, "C": 2},
        mutex_pairs={frozenset({"a1", "c1"}), frozenset({"b1", "b2"})},
    )
    epsilon = 1.2
    sensitivity = float(utility_sensitivity_fixed_size(candidates))
    return ExponentialMechanism(candidates, constraints, epsilon, sensitivity)


def main() -> None:
    mech = build_demo_mechanism()
    print("=== Subset exponential mechanism demo (synthetic data) ===")
    print(f"epsilon           = {mech.epsilon}")
    print(f"sensitivity       = {mech.sensitivity}  (max-min score range, fixed-size sum utility)")
    print(f"subset_size       = {mech.constraints.subset_size}")
    print(f"group_quotas      = {dict(mech.constraints.group_quotas)}")
    print(f"mutex_pairs       = {sorted(sorted(p) for p in mech.constraints.mutex_pairs)}")
    print(f"legal subsets     = {len(mech.subsets)}")

    probs = mech.probabilities()
    ref = mech.probabilities_decimal()
    print("\n--- normalised distribution over legal subsets (float64 vs Decimal-50 ref) ---")
    order = sorted(range(len(probs)), key=lambda i: -probs[i])
    for rank, i in enumerate(order):
        ids = ",".join(c.cid for c in mech.subsets[i])
        print(
            f"  {rank:2d}. {{{ids:<12}}} u={mech.utilities[i]:>3}  "
            f"P={probs[i]:.8f}  ref={float(ref[i]):.8f}"
        )
    print(f"  sum(P) = {sum(probs)!r}")

    err = mech.numerical_error()
    print("\n--- explicit numerical error vs Decimal-50 reference ---")
    print(f"  max abs error = {err['max_abs_error']:.3e}")
    print(f"  max rel error = {err['max_rel_error']:.3e}")

    evidence = mech.sample(n=5, seed=20260927)
    print("\n--- auditable sampling (seed=20260927, n=5) ---")
    for d in evidence.draws:
        ids = ",".join(d.subset_ids)
        print(
            f"  draw {d.draw_index}: u={d.uniform:.12f} -> "
            f"subset#{d.subset_index} {{{ids}}} (P={d.subset_probability:.8f})"
        )
    print(f"  replay verifies: {evidence.replay()}")

    # Statistical sanity check only -- not a formal privacy proof.
    big = mech.sample(n=20000, seed=7)
    counts = Counter(d.subset_index for d in big.draws)
    worst = max(
        abs(counts.get(i, 0) / len(big.draws) - p)
        for i, p in enumerate(big.probabilities)
    )
    print("\n--- statistical frequency check (20000 draws, NOT a privacy proof) ---")
    print(f"  max |empirical freq - exact P| = {worst:.4f}")


if __name__ == "__main__":
    main()
