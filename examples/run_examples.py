"""Runnable examples for bootstrap_ci (Python 3.11, standard library only).

Run from the repository root:
    python3.11 examples/run_examples.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from bootstrap_ci import (
    DegenerateJackknifeError,
    IIDPlan,
    InvalidResampleError,
    MovingBlockPlan,
    PairedPlan,
    RandomSource,
    StratifiedPlan,
    bootstrap,
    bootstrap_exact,
)


def mean(values):
    return sum(values) / len(values)


def main():
    print("=== 1. IID sample: percentile vs BCa (Monte Carlo, empirical) ===")
    data = [2.1, 2.5, 3.3, 3.7, 4.1, 4.4, 4.9, 5.2, 5.8, 6.6, 7.1, 8.3]
    plan = IIDPlan(data)
    rng = RandomSource(seed=20261002)
    print(bootstrap(plan, mean, n_resamples=5000, rng=rng, method="percentile"))
    print(bootstrap(plan, mean, n_resamples=5000, rng=rng, method="bca"))

    print("\n=== 2. Paired sample: pairs are resampled jointly ===")
    before = [68.0, 72.5, 70.1, 75.3, 69.8, 71.2, 74.0, 73.1]
    after = [66.9, 70.8, 69.5, 72.6, 68.7, 70.0, 72.9, 71.8]
    pairs = list(zip(before, after))
    mean_diff = lambda ps: sum(a - b for a, b in ps) / len(ps)
    print(bootstrap(PairedPlan(pairs), mean_diff, n_resamples=5000,
                    rng=RandomSource(11), method="bca"))

    print("\n=== 3. Stratified groups: resampling stays within strata ===")
    strata = {"control": [1.2, 1.5, 1.8, 2.0, 2.3],
              "treatment": [2.1, 2.4, 2.9, 3.1, 3.6]}
    effect = lambda s: mean(s["treatment"]) - mean(s["control"])
    print(bootstrap(StratifiedPlan(strata), effect, n_resamples=5000,
                    rng=RandomSource(12)))

    print("\n=== 4. Moving block bootstrap for an autocorrelated series ===")
    series, value, state = [], 0.0, 987654321
    for _ in range(80):
        state = (1103515245 * state + 12345) % (2 ** 31)
        value = 0.85 * value + (state / 2 ** 31 - 0.5)
        series.append(value)
    plan = MovingBlockPlan(series, block_length=6, boundary="circular")
    print(bootstrap(plan, mean, n_resamples=5000, rng=RandomSource(13)))

    print("\n=== 5. Exact enumeration (exact, not an estimate) vs Monte Carlo ===")
    small = IIDPlan([1.0, 2.0, 3.0, 4.0, 5.0])
    print(bootstrap_exact(small, mean, alpha=0.05))
    print(bootstrap(small, mean, n_resamples=50_000, rng=RandomSource(14),
                    alpha=0.05))

    print("\n=== 6. Degenerate sample: identical values -> point interval ===")
    print(bootstrap(IIDPlan([4.2] * 8), mean, n_resamples=1000,
                    rng=RandomSource(15)))
    try:
        bootstrap(IIDPlan([4.2] * 8), mean, n_resamples=1000,
                  rng=RandomSource(15), method="bca")
    except DegenerateJackknifeError as exc:
        print(f"BCa fails explicitly: {exc}")

    print("\n=== 7. Invalid replicates are never silently dropped ===")

    def fragile_statistic(values):
        if len(set(values)) == 1:
            return float("nan")  # undefined for a degenerate resample
        m = mean(values)
        return sum((v - m) ** 2 for v in values) / (len(values) - 1)

    fragile = IIDPlan([1.0, 1.0, 2.0, 3.0])
    try:
        bootstrap(fragile, fragile_statistic, n_resamples=500,
                  rng=RandomSource(16))
    except InvalidResampleError as exc:
        print(f"default policy 'raise': {exc}")
    result = bootstrap(fragile, fragile_statistic, n_resamples=500,
                       rng=RandomSource(16), invalid_policy="omit")
    print(f"explicit policy 'omit': {result}")
    print(f"  -> effective B = {result.effective_resamples} "
          f"(invalid_count={result.invalid_count} is reported, not hidden)")


if __name__ == "__main__":
    main()
