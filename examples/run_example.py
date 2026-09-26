"""End-to-end demo on synthetic data (no real data is used anywhere).

Run from the repository root:

    python3.11 examples/run_example.py
"""

import math
import os
import random
import sys
from fractions import Fraction

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from dpcount import DiscreteLaplaceMechanism, LaplaceMechanism


def main() -> None:
    print("=" * 72)
    print("Synthetic subject-level counting with formal DP checks")
    print("=" * 72)

    # ------------------------------------------------------------------
    # Synthetic scenario (definitions and parameters)
    # ------------------------------------------------------------------
    # * 60 synthetic subjects, each contributing up to 2 synthetic records
    #   -> subject-level sensitivity Delta = 2 for the count query.
    # * Adjacency: add/remove one subject.
    # * Finite output domain: counts in {0, ..., 120}.
    n_subjects, max_records_per_subject = 60, 2
    sensitivity = max_records_per_subject
    max_count = n_subjects * max_records_per_subject
    epsilon = 1.0

    rng = random.Random(20260927)
    # Synthetic dataset: subject i contributes 0, 1 or 2 records.
    records_per_subject = [rng.randrange(max_records_per_subject + 1)
                           for _ in range(n_subjects)]
    true_count = sum(records_per_subject)
    print(f"\nSynthetic data: {n_subjects} subjects, <= {max_records_per_subject} "
          f"records each -> true count = {true_count}")
    print(f"Parameters: epsilon = {epsilon}, sensitivity (Delta) = {sensitivity}, "
          f"output domain = {{0, ..., {max_count}}}")

    # ------------------------------------------------------------------
    # 1. Continuous Laplace (baseline interface; numerical approximation)
    # ------------------------------------------------------------------
    print("\n--- Continuous Laplace mechanism (float; NUMERICAL APPROXIMATION) ---")
    lap = LaplaceMechanism(sensitivity=sensitivity, epsilon=epsilon,
                           uniform=random.Random(42).random)
    for i in range(3):
        res = lap.noisy_count(true_count)
        print(f"  draw {i}: raw = {res.raw:+.6f}  "
              f"post-processed (clamped >= 0) = {res.postprocessed:.6f}  "
              f"exact = {res.exact}")
    print("  note: raw and post-processed values are recorded separately;")
    print("        negative raw values are kept as-is (never resampled).")

    # ------------------------------------------------------------------
    # 2. Discrete Laplace on a finite domain (exact rational probabilities)
    # ------------------------------------------------------------------
    print("\n--- Discrete Laplace on finite domain (exact rational mechanism) ---")
    mech = DiscreteLaplaceMechanism(
        max_count=max_count, sensitivity=sensitivity, epsilon=Fraction(epsilon),
        randbelow=random.Random(7).randrange)
    print(f"  rational base r = a/b with float(r) = {float(mech.r):.10f} "
          f"(r >= exp(-epsilon/Delta) = {math.exp(-epsilon / sensitivity):.10f})")
    print(f"  exact per-adjacency ratio bound r^-Delta = "
          f"{float(mech.privacy_loss_bound()):.10f} <= e^epsilon = {math.e:.10f}")

    c = true_count
    probs = mech.probabilities(c)
    print(f"\n  Exact distribution around the true count c = {c} "
          f"(all probabilities are exact Fractions, sum = {sum(probs)}):")
    for y in range(c - 3, c + 4):
        print(f"    P({y}) = {float(probs[y]):.8f}")

    samples = [mech.sample(c) for _ in range(5)]
    print(f"\n  5 exact integer samples (seeded randbelow): {samples}")
    k_demo = 0
    print(f"  deterministic inverse CDF check: inverse_cdf(c={c}, k={k_demo}) "
          f"= {mech.inverse_cdf(c, k_demo)} (recomputable from the same k)")

    # ------------------------------------------------------------------
    # 3. Exhaustive formal verification (NOT a sampling experiment)
    # ------------------------------------------------------------------
    print("\n--- Exhaustive formal verification of the ratio bound ---")
    result = mech.verify_dp()
    print(f"  enumerated {result.pairs_checked} adjacent count pairs "
          f"(stride = Delta = {result.stride}) x {max_count + 1} outputs "
          f"= {result.outputs_checked} exact rational checks")
    print(f"  max observed ratio P(y|c')/P(y|c) = {float(result.max_ratio):.10f}")
    print(f"  claimed bound r^-Delta            = {float(result.bound):.10f}")
    print(f"  RESULT: {'PASS' if result.ok else 'FAIL'} "
          f"(exact Fraction comparisons, no sampling involved)")

    # ------------------------------------------------------------------
    # 4. Sampling-frequency experiment -- explicitly NOT a privacy proof
    # ------------------------------------------------------------------
    print("\n--- Sampling-frequency experiment (illustration only) ---")
    print("  NOTE: this compares empirical frequencies with exact probabilities")
    print("  as a sanity check of the sampler.  It is NOT a privacy proof;")
    print("  the privacy guarantee is the exhaustive check above.")
    n_draws = 20000
    mech2 = DiscreteLaplaceMechanism(
        max_count=max_count, sensitivity=sensitivity, epsilon=Fraction(epsilon),
        randbelow=random.Random(99).randrange)
    freq = [0] * (max_count + 1)
    for _ in range(n_draws):
        freq[mech2.sample(c)] += 1
    exact = mech2.probabilities(c)
    max_abs_err = max(abs(freq[y] / n_draws - float(exact[y]))
                      for y in range(max_count + 1))
    print(f"  {n_draws} draws, max |empirical freq - exact probability| "
          f"= {max_abs_err:.5f}")


if __name__ == "__main__":
    main()
