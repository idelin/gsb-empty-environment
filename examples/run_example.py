"""End-to-end demo: ECDF frequencies, TV / KS statistics, and permutation
calibration (exact, Monte Carlo, paired, refusal, parallel sharding)."""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from ecdfcal import (
    ECDF,
    Design,
    Statistic,
    calibrate,
    frequency_table,
    ks_statistic,
    tv_distance,
)


def show_ecdf(name, samples):
    ecdf = ECDF.from_samples(samples)
    print(f"{name}: n={ecdf.n_observations} (sorted support with ties)")
    print(f"  {'value':>6} {'mult':>5} {'mass':>8} {'F(x)':>8}")
    for value, mult, mass, cum in frequency_table(ecdf):
        print(f"  {value:>6} {mult:>5} {float(mass):>8.4f} {float(cum):>8.4f}")
    return ecdf


def show_result(title, result):
    print(f"{title}")
    print(f"  observed {result.statistic.value} = {result.observed} "
          f"(~{float(result.observed):.4f})")
    if result.refused:
        print(f"  p-value: REFUSED ({result.refusal_reason})")
        return
    print(f"  kind = {result.kind}, exceedances = {result.exceedances}"
          f" / {result.reference_size}")
    print(f"  p-value = {result.p_value} (~{float(result.p_value):.4f})")
    if result.kind == "monte_carlo":
        print(f"  {result.confidence:.0%} Clopper-Peerson interval for the "
              f"exceedance rate: [{result.ci_low:.4f}, {result.ci_high:.4f}]")
        print(f"  injected RNG seed = {result.seed}, "
              f"draws = {result.reference_size}")


def main():
    print("== 1. ECDF with ties (simultaneous jumps, full multiplicity) ==")
    sample_a = [1, 2, 2, 3, 5]
    sample_b = [2, 3, 3, 4, 4, 6]
    ecdf_a = show_ecdf("sample A", sample_a)
    ecdf_b = show_ecdf("sample B", sample_b)
    print(f"KS  = {ks_statistic(ecdf_a, ecdf_b)}")
    print(f"TV  = {tv_distance(ecdf_a, ecdf_b)}")
    print()

    print("== 2. Exact unpaired permutation calibration (all C(11,5)=462 "
          "re-labellings) ==")
    exact = calibrate(sample_a, sample_b,
                      design=Design.UNPAIRED_EXCHANGEABLE,
                      statistic=Statistic.KS, method="exact")
    show_result("exact, KS:", exact)
    print()

    print("== 3. Monte Carlo calibration (caller-injected RNG, seed recorded) ==")
    mc = calibrate(sample_a, sample_b,
                   design=Design.UNPAIRED_EXCHANGEABLE,
                   statistic=Statistic.KS, method="monte_carlo",
                   n_permutations=5000, seed=20261002)
    show_result("monte_carlo, KS:", mc)
    print()

    print("== 4. Paired design: exact sign-flip calibration (2^6 = 64) ==")
    before = [5, 3, 4, 2, 6, 4]
    after = [7, 3, 6, 2, 9, 5]
    paired = calibrate(before, after,
                       design=Design.PAIRED_EXCHANGEABLE,
                       statistic=Statistic.KS, method="exact")
    show_result("paired exact, KS:", paired)
    print()

    print("== 5. Non-exchangeable design: calibrated p-value refused ==")
    refused = calibrate(sample_a, sample_b, design=Design.NOT_EXCHANGEABLE)
    show_result("not exchangeable:", refused)
    print()

    print("== 6. Rank-sharded parallel enumeration == serial enumeration ==")
    serial = calibrate(sample_a, sample_b,
                       design=Design.UNPAIRED_EXCHANGEABLE, method="exact")
    parallel = calibrate(sample_a, sample_b,
                         design=Design.UNPAIRED_EXCHANGEABLE, method="exact",
                         n_shards=4, parallel=True)
    print(f"  serial   p = {serial.p_value} over {serial.reference_size} ranks")
    print(f"  parallel p = {parallel.p_value} over 4 disjoint rank shards")
    print(f"  identical: {serial == parallel}")
    print()

    print("== 7. Label reordering invariance ==")
    reordered = calibrate(list(reversed(sample_a)), list(reversed(sample_b)),
                          design=Design.UNPAIRED_EXCHANGEABLE, method="exact")
    print(f"  reordered-input p = {reordered.p_value} "
          f"(identical: {reordered == exact})")


if __name__ == "__main__":
    main()
