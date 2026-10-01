"""Runnable examples for the permtest package.

Run from the repository root:

    python3.11 examples/run_examples.py
"""
import os
import random
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from permtest import (
    ExchangeabilityError,
    ecdf_points,
    frequencies,
    grouped_calibration,
    ks_statistic,
    paired_calibration,
    total_variation,
)


def show(title, result):
    label = "EXACT" if result.exact else "MONTE CARLO ESTIMATE"
    print(f"  [{label}] {title}")
    print(f"    statistic={result.statistic} observed={result.observed:.6f}")
    print(f"    p_value={result.p_value:.6f} "
          f"({result.exceedances}/{result.num_permutations} permutations)")
    if result.mc_interval is not None:
        lo, hi = result.mc_interval
        print(f"    {1 - result.alpha:.0%} Clopper-Pearson interval: "
              f"[{lo:.6f}, {hi:.6f}]")
    print(f"    seed={result.seed} sizes={result.sizes}")


def main():
    print("== Descriptive statistics (discrete samples, ties jump together) ==")
    sample_a = [1, 1, 1, 2, 3, 3, 5]
    sample_b = [2, 2, 3, 4, 4, 5]
    print(f"  frequencies(a) = {dict(frequencies(sample_a))}")
    print(f"  frequencies(b) = {dict(frequencies(sample_b))}")
    print(f"  ecdf_points(a) = {ecdf_points(sample_a)}")
    print(f"  total_variation = {total_variation(sample_a, sample_b):.6f}")
    print(f"  ks_statistic    = {ks_statistic(sample_a, sample_b):.6f}")
    print()

    print("== Grouped design, small sample: exact full-permutation calibration ==")
    show("grouped exact KS", grouped_calibration(sample_a, sample_b, "ks"))
    show("grouped exact TV (rank-sharded x4)",
         grouped_calibration(sample_a, sample_b, "tv", num_shards=4))
    print()

    print("== Grouped design, larger sample: Monte Carlo with injected RNG ==")
    rng = random.Random(0)
    big_a = [rng.randrange(6) for _ in range(15)]
    rng = random.Random(1)
    big_b = [rng.randrange(6) + 1 for _ in range(15)]
    show("grouped MC KS",
         grouped_calibration(big_a, big_b, "ks", rng=random.Random(20261001),
                             seed=20261001, num_mc=20000))
    print()

    print("== Paired design: exact sign-flip calibration ==")
    before = [3, 1, 4, 1, 5, 9, 2, 6]
    after = [4, 2, 4, 3, 6, 8, 3, 5]
    show("paired exact |mean diff|",
         paired_calibration(before, after, "mean_diff"))
    print()

    print("== Non-exchangeable design: calibration refused ==")
    try:
        grouped_calibration(sample_a, sample_b, exchangeable=False)
    except ExchangeabilityError as exc:
        print(f"  refused: {exc}")


if __name__ == "__main__":
    main()
