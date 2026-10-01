"""Runnable examples for the permtest library.

Run from the repository root:

    python3 examples/run_examples.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from permtest import paired_test, stratified_test, two_sample_test


def show(title, result):
    print(f"--- {title} ---")
    print(f"  method              : {result.method}")
    print(f"  design              : {result.design}")
    print(f"  observed statistic  : {result.observed_statistic:.6f}")
    if hasattr(result, "p_value"):
        print(f"  exact p-value       : {result.p_value:.6f}"
              f"  ({result.n_extreme}/{result.n_total} permutations)")
    else:
        print(f"  p estimate          : {result.p_estimate:.6f}"
              f"  [{result.ci_low:.6f}, {result.ci_high:.6f}]"
              f"  ({result.confidence:.0%} Wilson CI,"
              f" {result.draws} draws, seed={result.seed})")
    print()


def main():
    # 1. Small independent two-sample: exact enumeration of all C(6,3)=20
    #    label assignments, original arrangement included.
    show("independent two-sample, exact",
         two_sample_test([8.1, 7.9, 8.4], [6.2, 5.8, 6.6]))

    # 2. Unequal group sizes with tied values: duplicates keep their
    #    multiplicity because permutations range over observation indices.
    show("unequal groups + ties, exact",
         two_sample_test([2, 2, 5], [1, 2], alternative="greater"))

    # 3. Paired sign-flip test (2^5 = 32 arrangements).
    show("paired sign-flip, exact",
         paired_test(before=[10, 12, 9, 11, 10],
                     after=[11, 13, 10, 14, 12]))

    # 4. Stratified: labels permute within each stratum only.
    show("stratified, exact",
         stratified_test([([8, 9], [5, 6]), ([7, 8, 9], [4, 5])]))

    # 5. Large space: Monte Carlo with caller-injected seed; the result is
    #    labelled approximate and carries an explicit error interval.
    group1 = [x + 2.0 for x in range(20)]
    group2 = [float(x) for x in range(20)]
    show("independent two-sample, Monte Carlo",
         two_sample_test(group1, group2, method="monte-carlo",
                         draws=20_000, seed=20261001))

    # 6. Sharded exact enumeration with checkpointing (resumable).
    result = two_sample_test([9, 8, 7, 6, 5], [1, 2, 3, 4, 0], method="exact",
                             n_shards=4,
                             checkpoint_path="/tmp/permtest_ckpt.json")
    show("exact, 4 shards + checkpoint", result)


if __name__ == "__main__":
    main()
