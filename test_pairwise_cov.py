"""Unittests for pairwise_cov (Python 3.11, stdlib only)."""

import math
import unittest
from fractions import Fraction

from pairwise_cov import (
    ConflictingBatchError,
    CovarianceLedger,
    PairMoment,
    ShardAccumulator,
    UnknownBatchError,
    dense_pairwise_covariance,
    psd_check,
    quadratic_form,
)


def assert_matrix_close(case, actual, expected, places=9):
    case.assertEqual(len(actual), len(expected))
    for row_a, row_e in zip(actual, expected):
        case.assertEqual(len(row_a), len(row_e))
        for a, e in zip(row_a, row_e):
            if math.isnan(e):
                case.assertTrue(math.isnan(a), f"expected NaN, got {a}")
            else:
                case.assertAlmostEqual(a, e, places=places)


class TestMissingValues(unittest.TestCase):
    def test_missing_is_not_zero(self):
        # If None were coerced to 0, count(0,1) would be 2 and cov defined.
        acc = ShardAccumulator(2)
        acc.add_row([1.0, None])
        acc.add_row([2.0, 4.0])
        self.assertEqual(acc.count(0, 1), 1)
        self.assertTrue(math.isnan(acc.covariance(0, 1)))
        self.assertEqual(acc.count(0, 0), 2)
        self.assertEqual(acc.count(1, 1), 1)

    def test_nan_is_missing(self):
        acc = ShardAccumulator(2)
        acc.add_row([1.0, float("nan")])
        acc.add_row([2.0, 3.0])
        self.assertEqual(acc.count(0, 1), 1)

    def test_zero_is_a_real_observation(self):
        acc = ShardAccumulator(1)
        acc.add_row([0.0])
        acc.add_row([None])
        self.assertEqual(acc.count(0, 0), 1)

    def test_non_numeric_rejected(self):
        acc = ShardAccumulator(1)
        with self.assertRaises(TypeError):
            acc.add_row(["x"])
        with self.assertRaises(TypeError):
            acc.add_row([True])


class TestDenominatorRule(unittest.TestCase):
    def test_single_common_observation_undefined(self):
        # Two columns sharing exactly one row: sample covariance undefined.
        acc = ShardAccumulator(2)
        acc.add_row([1.0, 2.0])
        acc.add_row([3.0, None])
        acc.add_row([None, 4.0])
        self.assertEqual(acc.count(0, 1), 1)
        self.assertTrue(math.isnan(acc.covariance(0, 1, ddof=1)))
        # Population covariance (ddof=0) IS defined and equals 0 here.
        self.assertEqual(acc.covariance(0, 1, ddof=0), 0.0)

    def test_ddof_denominators(self):
        acc = ShardAccumulator(1)
        for v in (1.0, 2.0, 3.0):
            acc.add_row([v])
        self.assertAlmostEqual(acc.covariance(0, 0, ddof=1), 1.0)
        self.assertAlmostEqual(acc.covariance(0, 0, ddof=0), 2.0 / 3.0)

    def test_negative_ddof_rejected(self):
        acc = ShardAccumulator(1)
        acc.add_row([1.0])
        with self.assertRaises(ValueError):
            acc.covariance(0, 0, ddof=-1)


class TestNumericalStability(unittest.TestCase):
    def test_huge_mean_tiny_variance(self):
        # Naive sum-of-squares cancels catastrophically; Welford must not.
        offset = 1e12
        xs = [offset + k for k in range(5)]
        ys = [offset - 2 * k for k in range(5)]
        acc = ShardAccumulator(2)
        for x, y in zip(xs, ys):
            acc.add_row([x, y])

        # Exact reference with rational arithmetic.
        fx = [Fraction(v) for v in xs]
        fy = [Fraction(v) for v in ys]
        mx = sum(fx) / 5
        my = sum(fy) / 5
        exact_c = sum((x - mx) * (y - my) for x, y in zip(fx, fy))
        exact_cov = float(exact_c / 4)  # = -5.0
        exact_var = float(sum((x - mx) ** 2 for x in fx) / 4)  # = 2.5

        self.assertAlmostEqual(acc.covariance(0, 1), exact_cov, delta=1e-6)
        self.assertAlmostEqual(acc.covariance(0, 0), exact_var, delta=1e-6)

        # Demonstrate the naive formula really does fail at this scale.
        n = len(xs)
        mean = sum(xs) / n
        naive_var = (sum(x * x for x in xs) - n * mean * mean) / (n - 1)
        self.assertGreater(abs(naive_var - exact_var), 1.0)

    def test_shard_merge_matches_direct(self):
        rows = [
            [1.0, None, 3.0],
            [2.0, 5.0, None],
            [None, 6.0, 1.0],
            [4.0, 7.0, 2.0],
            [5.0, None, None],
            [6.0, 9.0, 4.0],
        ]
        shard_a = ShardAccumulator(3)
        shard_a.add_rows(rows[:2])
        shard_b = ShardAccumulator(3)
        shard_b.add_rows(rows[2:4])
        shard_c = ShardAccumulator(3)
        shard_c.add_rows(rows[4:])
        merged = shard_a.merged(shard_b).merged(shard_c)

        direct = ShardAccumulator(3)
        direct.add_rows(rows)
        ref_cov, ref_counts = dense_pairwise_covariance(rows, 3)

        self.assertEqual(merged.pair_counts(), ref_counts)
        self.assertEqual(direct.pair_counts(), ref_counts)
        assert_matrix_close(self, merged.covariance_matrix(), ref_cov)
        assert_matrix_close(self, direct.covariance_matrix(), ref_cov)


class TestUnmerge(unittest.TestCase):
    def test_merge_unmerge_roundtrip(self):
        a = PairMoment()
        for x, y in [(1.0, 2.0), (3.0, 1.0), (5.0, 8.0)]:
            a.add(x, y)
        b = PairMoment()
        for x, y in [(2.0, 2.0), (4.0, 4.0)]:
            b.add(x, y)
        total = a.merged(b)
        recovered = total.without(b)
        self.assertEqual(recovered.n, a.n)
        self.assertAlmostEqual(recovered.mean_x, a.mean_x)
        self.assertAlmostEqual(recovered.mean_y, a.mean_y)
        self.assertAlmostEqual(recovered.c, a.c)

    def test_unmerge_negative_count_rejected(self):
        a = PairMoment()
        a.add(1.0, 1.0)
        b = PairMoment()
        b.add(1.0, 1.0)
        b.add(2.0, 2.0)
        with self.assertRaises(ValueError):
            a.without(b)


class TestLedgerBatchOps(unittest.TestCase):
    def setUp(self):
        self.rows_a = [[1.0, 2.0, None], [3.0, 4.0, 5.0]]
        self.rows_b = [[None, 6.0, 7.0], [8.0, 9.0, 10.0], [11.0, None, 12.0]]
        self.rows_c = [[2.0, 3.0, 4.0], [5.0, 6.0, None]]

    def test_add_is_idempotent(self):
        ledger = CovarianceLedger(3)
        self.assertTrue(ledger.add_batch("a", self.rows_a))
        before = ledger.pair_counts()
        self.assertFalse(ledger.add_batch("a", self.rows_a))
        self.assertEqual(ledger.batch_ids(), ["a"])
        self.assertEqual(ledger.pair_counts(), before)

    def test_add_same_id_different_payload_rejected(self):
        ledger = CovarianceLedger(3)
        ledger.add_batch("a", self.rows_a)
        with self.assertRaises(ConflictingBatchError):
            ledger.add_batch("a", self.rows_b)

    def test_retract_nonexistent_rejected(self):
        ledger = CovarianceLedger(3)
        with self.assertRaises(UnknownBatchError):
            ledger.retract_batch("nope")

    def test_retract_equals_recompute_and_order_independent(self):
        ledger = CovarianceLedger(3)
        ledger.add_batch("a", self.rows_a)
        ledger.add_batch("b", self.rows_b)
        ledger.add_batch("c", self.rows_c)
        all_rows = self.rows_a + self.rows_b + self.rows_c

        # Independent direct computation over all data.
        ref_cov, ref_counts = dense_pairwise_covariance(all_rows, 3)
        self.assertEqual(ledger.pair_counts(), ref_counts)
        assert_matrix_close(self, ledger.covariance_matrix(), ref_cov)

        # Retract 'b': result must equal a fresh ledger with a,c only.
        record = ledger.retract_batch("b")
        self.assertEqual(record.row_count, len(self.rows_b))
        fresh = CovarianceLedger(3)
        fresh.add_batch("a", self.rows_a)
        fresh.add_batch("c", self.rows_c)
        ref_cov2, ref_counts2 = dense_pairwise_covariance(
            self.rows_a + self.rows_c, 3
        )
        self.assertEqual(ledger.pair_counts(), ref_counts2)
        self.assertEqual(fresh.pair_counts(), ref_counts2)
        assert_matrix_close(self, ledger.covariance_matrix(), ref_cov2)

        # Retraction order independence: c then a vs a then c.
        led1 = CovarianceLedger(3)
        led1.add_batch("a", self.rows_a)
        led1.add_batch("b", self.rows_b)
        led1.add_batch("c", self.rows_c)
        led1.retract_batch("c")
        led1.retract_batch("a")

        led2 = CovarianceLedger(3)
        led2.add_batch("a", self.rows_a)
        led2.add_batch("b", self.rows_b)
        led2.add_batch("c", self.rows_c)
        led2.retract_batch("a")
        led2.retract_batch("c")
        self.assertEqual(led1.pair_counts(), led2.pair_counts())
        assert_matrix_close(self, led1.covariance_matrix(), led2.covariance_matrix())

        # Counts never negative, audit trail retained.
        self.assertTrue(all(c >= 0 for row in led1.pair_counts() for c in row))
        self.assertEqual(len(led1.retraction_log), 2)
        self.assertEqual(
            [r.batch_id for r in led1.retraction_log], ["c", "a"]
        )
        self.assertTrue(ledger.verify().ok)

    def test_incremental_unmerge_matches_retraction(self):
        # The inverse-merge path must agree with the recompute-based path.
        ledger = CovarianceLedger(3)
        ledger.add_batch("a", self.rows_a)
        ledger.add_batch("b", self.rows_b)
        ledger.add_batch("c", self.rows_c)
        total = ledger.merged()
        remaining = total.without(ledger._batches["b"])
        ledger.retract_batch("b")
        assert_matrix_close(
            self,
            remaining.covariance_matrix(),
            ledger.covariance_matrix(),
        )


class TestExplicitDimension(unittest.TestCase):
    def test_bad_dimension_rejected(self):
        with self.assertRaises(ValueError):
            ShardAccumulator(0)
        with self.assertRaises(ValueError):
            ShardAccumulator(-1)

    def test_row_length_checked(self):
        acc = ShardAccumulator(3)
        with self.assertRaises(ValueError):
            acc.add_row([1.0, 2.0])

    def test_index_checked(self):
        acc = ShardAccumulator(2)
        acc.add_row([1.0, 2.0])
        with self.assertRaises(IndexError):
            acc.count(0, 2)
        with self.assertRaises(TypeError):
            acc.count(0, 1.0)


class TestPSDDiagnostics(unittest.TestCase):
    def test_non_psd_returns_witness(self):
        # Three pairs completed on disjoint row sets; pairwise-complete
        # correlations form [[1,+1,+1],[+1,1,-1],[+1,-1,1]] whose
        # eigenvalues are -1, +2, +2.
        rows = [
            [1.0, 1.0, None],
            [2.0, 2.0, None],
            [3.0, 3.0, None],
            [None, 1.0, -1.0],
            [None, 2.0, -2.0],
            [None, 3.0, -3.0],
            [1.0, None, 1.0],
            [2.0, None, 2.0],
            [3.0, None, 3.0],
        ]
        cov, counts = dense_pairwise_covariance(rows, 3)
        self.assertEqual(
            counts,
            [[6, 3, 3], [3, 6, 3], [3, 3, 6]],
        )
        report = psd_check(cov)
        self.assertTrue(report.determinate)
        self.assertFalse(report.is_psd)
        self.assertAlmostEqual(
            report.min_eigenvalue, -0.5264531609803356, places=9
        )
        self.assertIsNotNone(report.witness)
        self.assertLess(report.witness_quadratic_form, 0.0)
        self.assertAlmostEqual(
            quadratic_form(cov, report.witness),
            report.min_eigenvalue,
            places=9,
        )

        # Matrix was not silently "fixed".
        self.assertAlmostEqual(cov[0][1], 1.0)
        self.assertAlmostEqual(cov[1][2], -1.0)

        # The ledger reaches the same diagnosis.
        ledger = CovarianceLedger(3)
        ledger.add_batch("rows", rows)
        ledger_report = psd_check(ledger.covariance_matrix())
        self.assertFalse(ledger_report.is_psd)
        self.assertAlmostEqual(
            ledger_report.min_eigenvalue, report.min_eigenvalue, places=9
        )

    def test_psd_for_complete_data(self):
        rows = [[1.0, 2.0, 3.0], [4.0, 5.0, 6.0], [7.0, 8.0, 9.0],
                [2.0, 8.0, 4.0]]
        cov, _ = dense_pairwise_covariance(rows, 3)
        report = psd_check(cov)
        self.assertTrue(report.determinate)
        self.assertTrue(report.is_psd)
        self.assertIsNone(report.witness)

    def test_undefined_entries_reported(self):
        matrix = [[1.0, float("nan")], [float("nan"), 1.0]]
        report = psd_check(matrix)
        self.assertFalse(report.determinate)
        self.assertEqual(report.undefined_pairs, [(0, 1)])


if __name__ == "__main__":
    unittest.main(verbosity=2)
