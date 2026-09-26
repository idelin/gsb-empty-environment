"""Run sample: exponential mechanism over legal candidate subsets.

Synthetic data only.  Run with:  python3.11 demo.py
"""

import math
from collections import Counter

from exponential_mechanism import (
    Candidate,
    Constraints,
    ExponentialMechanism,
    InfeasibleError,
)


def section(title):
    print(f"\n=== {title} ===")


def main():
    section("1. Synthetic scenario: quotas + mutex pair")
    candidates = [
        Candidate("a1", 6, "A"), Candidate("a2", 4, "A"), Candidate("a3", 2, "A"),
        Candidate("b1", 5, "B"), Candidate("b2", 3, "B"),
        Candidate("c1", 1, "C"),
    ]
    constraints = Constraints(
        budget=3,
        group_quotas={"A": 2, "B": 1},          # at most 2 from A, 1 from B
        mutex_pairs={frozenset({"a1", "b1"})},  # a1 and b1 cannot co-occur
    )
    epsilon, sensitivity = 1.0, 4.0
    print(f"epsilon={epsilon}, sensitivity={sensitivity}, "
          f"weight = exp(eps * sum(score) / (2*sens))")
    mech = ExponentialMechanism(candidates, constraints, epsilon, sensitivity)
    exact = mech.exact_probabilities()
    probs = mech.probabilities()
    print(f"legal domain size: {len(mech.subsets)} subsets (exhaustive enumeration)")
    print(f"{'subset':<22}{'utility':>8}{'p_float':>14}{'p_exact(50d)':>22}{'abs err':>12}")
    for subset in mech.subsets:
        pe = float(exact[subset])
        pf = probs[subset]
        print(f"{','.join(subset):<22}{mech.utilities[subset]:>8}"
              f"{pf:>14.10f}{pe:>22.10f}{abs(pf-pe):>12.2e}")
    print(f"float max abs error vs exact reference: {mech.max_probability_error():.2e}")

    section("2. Auditable seeded draws (evidence + replay)")
    for seed in (1, 7, 7):
        rec = mech.sample(seed=seed)
        print(f"seed={rec.seed:>2}  draw={rec.draw:.12f}  ->  {','.join(rec.subset)}  "
              f"(P={rec.subset_probability:.6f}, interval "
              f"[{rec.cumulative_before:.6f},{rec.cumulative_after:.6f}), "
              f"replay_ok={mech.verify(rec)})")

    section("3. Equal scores => uniform over legal subsets")
    flat = [Candidate(cid, 5, "X") for cid in "wxyz"]
    flat_mech = ExponentialMechanism(
        flat, Constraints(budget=2), epsilon=0.9, sensitivity=1.0
    )
    ps = set(flat_mech.probabilities().values())
    print(f"domain={flat_mech.probabilities().__len__()} subsets, "
          f"all probabilities identical: {len(ps) == 1}, value={ps.pop():.10f}")

    section("4. sensitivity = 0 => explicit deterministic argmax rule")
    zero_mech = ExponentialMechanism(
        [Candidate("lo", 1), Candidate("hi", 9), Candidate("tie", 9)],
        Constraints(budget=1), epsilon=1.0, sensitivity=0.0,
    )
    print(zero_mech.probabilities())

    section("5. Extreme finite integer scores (log-space, no overflow)")
    extreme = [Candidate("lo", -10**9), Candidate("mid", 0), Candidate("hi", 10**9)]
    ext_mech = ExponentialMechanism(
        extreme, Constraints(budget=1), epsilon=10.0, sensitivity=1.0
    )
    print(ext_mech.probabilities())

    section("6. Infeasible domain => explicit error, candidates never truncated")
    try:
        ExponentialMechanism(
            [Candidate("x", 1), Candidate("y", 2)],
            Constraints(budget=2, mutex_pairs={frozenset({"x", "y"})}),
            epsilon=1.0, sensitivity=1.0,
        )
    except InfeasibleError as exc:
        print(f"InfeasibleError: {exc}")

    section("7. Frequency experiment (statistical sanity check, not a privacy proof)")
    freq_mech = ExponentialMechanism(
        [Candidate("a", 0), Candidate("b", 1)],
        Constraints(budget=1), epsilon=math.log(3.0), sensitivity=0.5,
    )
    trials = 20_000
    counts = Counter(freq_mech.sample(seed=i).subset for i in range(trials))
    for subset, count in sorted(counts.items()):
        print(f"{subset}: empirical={count/trials:.4f}  "
              f"exact={freq_mech.probabilities()[subset]:.4f}")


if __name__ == "__main__":
    main()
