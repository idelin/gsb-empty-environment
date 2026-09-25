"""Runnable example for pairwise_cov (Python 3.11, stdlib only)."""

import math

from pairwise_cov import (
    ConflictingBatchError,
    CovarianceLedger,
    ShardAccumulator,
    UnknownBatchError,
    dense_pairwise_covariance,
    psd_check,
)


def show(title, matrix):
    print(title)
    for row in matrix:
        print("  [" + ", ".join(f"{v: .6f}" if isinstance(v, float) and not math.isnan(v) else str(v) for v in row) + "]")


def main():
    print("=" * 72)
    print("1. Sparse batches -> pairwise-complete counts and covariance")
    print("=" * 72)
    ledger = CovarianceLedger(dim=3)
    ledger.add_batch("shard-a", [[1.0, 2.0, None], [3.0, 4.0, 5.0]])
    ledger.add_batch("shard-b", [[None, 6.0, 7.0], [8.0, 9.0, 10.0]])
    ledger.add_batch("shard-c", [[2.0, None, 4.0], [5.0, 6.0, None]])
    show("pair counts n_ij (missing never counted as zero):", ledger.pair_counts())
    show("sample covariance (ddof=1):", ledger.covariance_matrix())

    print()
    print("=" * 72)
    print("2. Shard merge == direct dense computation")
    print("=" * 72)
    all_rows = [
        [1.0, 2.0, None], [3.0, 4.0, 5.0],
        [None, 6.0, 7.0], [8.0, 9.0, 10.0],
        [2.0, None, 4.0], [5.0, 6.0, None],
    ]
    ref_cov, ref_counts = dense_pairwise_covariance(all_rows, 3)
    merged_cov = ledger.covariance_matrix()
    max_diff = max(
        abs(a - b)
        for ra, rb in zip(merged_cov, ref_cov)
        for a, b in zip(ra, rb)
        if not (math.isnan(a) and math.isnan(b))
    )
    print(f"counts equal: {ledger.pair_counts() == ref_counts}")
    print(f"max |merged - direct| = {max_diff:.3e}")
    report = ledger.verify()
    print(f"ledger.verify(): ok={report.ok} ({report.message})")

    print()
    print("=" * 72)
    print("3. Batch idempotency and retraction")
    print("=" * 72)
    added = ledger.add_batch("shard-a", [[1.0, 2.0, None], [3.0, 4.0, 5.0]])
    print(f"re-add identical 'shard-a' -> added={added} (idempotent no-op)")
    try:
        ledger.add_batch("shard-a", [[9.0, 9.0, 9.0]])
    except ConflictingBatchError as exc:
        print(f"conflicting re-add rejected: {exc}")
    rec = ledger.retract_batch("shard-b")
    print(f"retracted 'shard-b': rows={rec.row_count} "
          f"pair_observations={rec.pair_observations} digest={rec.digest[:12]}...")
    try:
        ledger.retract_batch("shard-b")
    except UnknownBatchError as exc:
        print(f"second retraction rejected: {exc}")
    show("counts after retraction (never negative):", ledger.pair_counts())
    fresh = CovarianceLedger(dim=3)
    fresh.add_batch("shard-a", [[1.0, 2.0, None], [3.0, 4.0, 5.0]])
    fresh.add_batch("shard-c", [[2.0, None, 4.0], [5.0, 6.0, None]])
    same = all(
        (math.isnan(x) and math.isnan(y)) or abs(x - y) < 1e-12
        for rx, ry in zip(ledger.covariance_matrix(), fresh.covariance_matrix())
        for x, y in zip(rx, ry)
    )
    print(f"retract == fresh recompute of remaining batches: {same}")

    print()
    print("=" * 72)
    print("4. Huge mean, tiny variance: Welford/Chan vs naive sum-of-squares")
    print("=" * 72)
    offset = 1e12
    xs = [offset + k for k in range(5)]
    acc = ShardAccumulator(1)
    for x in xs:
        acc.add_row([x])
    n = len(xs)
    mean = sum(xs) / n
    naive = (sum(x * x for x in xs) - n * mean * mean) / (n - 1)
    print(f"true sample variance = 2.5")
    print(f"naive sum-of-squares  = {naive!r}  (catastrophic cancellation)")
    print(f"Welford accumulator   = {acc.covariance(0, 0)!r}")

    print()
    print("=" * 72)
    print("5. Pairwise missingness can break positive semidefiniteness")
    print("=" * 72)
    rows = [
        [1.0, 1.0, None], [2.0, 2.0, None], [3.0, 3.0, None],
        [None, 1.0, -1.0], [None, 2.0, -2.0], [None, 3.0, -3.0],
        [1.0, None, 1.0], [2.0, None, 2.0], [3.0, None, 3.0],
    ]
    bad = CovarianceLedger(dim=3)
    bad.add_batch("disjoint-pairs", rows)
    cov = bad.covariance_matrix()
    show("pairwise-complete covariance (unchanged):", cov)
    report = psd_check(cov)
    print(f"is_psd={report.is_psd}  min_eigenvalue={report.min_eigenvalue:.6f}")
    print(f"witness v = {[round(v, 6) for v in report.witness]}")
    print(f"v^T S v = {report.witness_quadratic_form:.6f}  (< 0 => non-PSD, reported not repaired)")

    print()
    print("=" * 72)
    print("6. Undefined covariance: one common observation only")
    print("=" * 72)
    tiny = CovarianceLedger(dim=2)
    tiny.add_batch("t1", [[1.0, 2.0], [3.0, None], [None, 4.0]])
    print(f"n_01 = {tiny.count(0, 1)} -> cov_01 = {tiny.covariance(0, 1)} (NaN: undefined)")
    undef = psd_check(tiny.covariance_matrix())
    print(f"psd_check: determinate={undef.determinate} undefined_pairs={undef.undefined_pairs}")


if __name__ == "__main__":
    main()
