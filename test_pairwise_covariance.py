"""Unittest suite for pairwise_covariance (stdlib only, Python 3.11+)."""

import math
import unittest
from fractions import Fraction

from pairwise_covariance import (
    PairStats,
    PairwiseCovariance,
    merge_stats,
    quadratic_form,
    subtract_stats,
)


def reference_pair_stats(rows, i, j):
    """Exact two-pass reference using Fraction arithmetic (skips missing)."""
    obs = [
        (Fraction(r[i]), Fraction(r[j]))
        for r in rows
        if r[i] is not None and r[j] is not None
    ]
    n = len(obs)
    if n == 0:
        return 0, None, None, None
    mx = sum(x for x, _ in obs) / n
    my = sum(y for _, y in obs) / n
    comoment = sum((x - mx) * (y - my) for x, y in obs)
    return n, mx, my, comoment


def reference_covariance(rows, i, j, ddof=1):
    n, _, _, comoment = reference_pair_stats(rows, i, j)
    if n <= ddof:
        return None
    return comoment / (n - ddof)


class TestMissingValues(unittest.TestCase):
    def test_missing_is_not_zero(self):
        rows = [[1.0, 10.0], [None, 20.0], [3.0, 30.0]]
        pc = PairwiseCovariance(2)
        pc.add_batch("b1", rows)
        # Column 0 has 2 observations {1, 3}; if None were treated as 0 the
        # count would be 3 and the variance would be 7/3 instead of 2.
        self.assertEqual(pc.count(0, 0), 2)
        self.assertEqual(pc.count(0, 1), 2)
        self.assertAlmostEqual(pc.covariance(0, 0), 2.0)  # var of {1, 3}
        self.assertAlmostEqual(pc.covariance(1, 1), 100.0)  # var of {10, 20, 30}
        self.assertAlmostEqual(pc.covariance(0, 1), 20.0)

    def test_pairwise_counts_differ_per_pair(self):
        rows = [
            [1.0, 1.0, None],
            [2.0, None, 2.0],
            [3.0, 4.0, 3.0],
            [None, 5.0, 4.0],
        ]
        pc = PairwiseCovariance(3)
        pc.add_batch("b", rows)
        self.assertEqual(pc.count(0, 1), 2)
        self.assertEqual(pc.count(0, 2), 2)
        self.assertEqual(pc.count(1, 2), 2)
        self.assertEqual(pc.count(0, 0), 3)

    def test_nan_and_inf_rejected_not_silent(self):
        pc = PairwiseCovariance(2)
        with self.assertRaises(ValueError):
            pc.add_batch("bad", [[float("nan"), 1.0]])
        with self.assertRaises(ValueError):
            pc.add_batch("bad2", [[float("inf"), 1.0]])


class TestDenominatorRules(unittest.TestCase):
    def test_single_common_observation_sample_cov_undefined(self):
        rows = [[1.0, 2.0], [3.0, None], [4.0, None]]
        pc = PairwiseCovariance(2)  # ddof=1 default
        pc.add_batch("b", rows)
        self.assertEqual(pc.count(0, 1), 1)
        self.assertIsNone(pc.covariance(0, 1))
        # Population covariance (ddof=0) IS defined for one observation.
        self.assertEqual(pc.covariance(0, 1, ddof=0), 0.0)

    def test_sample_vs_population_denominator(self):
        rows = [[1.0, 2.0], [2.0, 4.0], [3.0, 6.0], [4.0, 8.0]]
        pc = PairwiseCovariance(2)
        pc.add_batch("b", rows)
        sample = pc.covariance(0, 1)            # / (n-1)
        population = pc.covariance(0, 1, ddof=0)  # / n
        self.assertAlmostEqual(sample, 5.0 / 3.0 * 2)
        self.assertAlmostEqual(population, 2.5)
        self.assertAlmostEqual(sample / population, 4.0 / 3.0)

    def test_matrix_holes_are_none(self):
        rows = [[1.0, None], [2.0, None]]
        pc = PairwiseCovariance(2)
        pc.add_batch("b", rows)
        m = pc.matrix()
        self.assertIsNone(m[0][1])
        self.assertIsNone(m[1][0])
        self.assertIsNotNone(m[0][0])


class TestShardMerge(unittest.TestCase):
    ROWS = [
        [1.0, None, 3.5],
        [2.5, 4.0, None],
        [None, 1.5, 2.0],
        [4.0, 4.5, 1.0],
        [5.5, None, None],
        [6.0, 7.5, 8.5],
        [7.0, 0.5, None],
    ]

    def test_sharded_merge_equals_direct(self):
        direct = PairwiseCovariance(3)
        direct.add_batch("all", self.ROWS)

        sharded = PairwiseCovariance(3)
        sharded.add_batch("s1", self.ROWS[:2])
        sharded.add_batch("s2", self.ROWS[2:5])
        sharded.add_batch("s3", self.ROWS[5:])

        for i in range(3):
            for j in range(3):
                self.assertEqual(direct.count(i, j), sharded.count(i, j))
                a, b = direct.covariance(i, j), sharded.covariance(i, j)
                self.assertEqual(a is None, b is None)
                if a is not None:
                    self.assertAlmostEqual(a, b, places=12)

    def test_merge_stats_matches_exact_fraction_reference(self):
        rows_a = [(1.5, 2.5), (3.5, 4.5), (5.5, 6.5)]
        rows_b = [(7.5, 8.5), (9.5, 1.5)]
        sa, sb = PairStats(), PairStats()
        for x, y in rows_a:
            sa.update(x, y)
        for x, y in rows_b:
            sb.update(x, y)
        merged = merge_stats(sa, sb)

        allrows = [[x, y] for x, y in rows_a + rows_b]
        n, mx, my, com = reference_pair_stats(allrows, 0, 1)
        self.assertEqual(merged.count, n)
        self.assertAlmostEqual(merged.mean_x, float(mx), places=12)
        self.assertAlmostEqual(merged.mean_y, float(my), places=12)
        self.assertAlmostEqual(merged.comoment, float(com), places=9)


class TestRetraction(unittest.TestCase):
    ROWS_A = [[1.0, 2.0], [3.0, None], [5.0, 6.0]]
    ROWS_B = [[7.0, 8.0], [None, 1.0], [2.0, 4.0]]
    ROWS_C = [[9.0, None], [1.0, 3.0]]

    def test_retract_restores_direct_computation(self):
        pc = PairwiseCovariance(2)
        pc.add_batch("A", self.ROWS_A)
        pc.add_batch("B", self.ROWS_B)
        pc.add_batch("C", self.ROWS_C)
        pc.remove_batch("B")

        direct = PairwiseCovariance(2)
        direct.add_batch("rest", self.ROWS_A + self.ROWS_C)
        for i in range(2):
            for j in range(2):
                self.assertEqual(pc.count(i, j), direct.count(i, j))
                a, b = pc.covariance(i, j), direct.covariance(i, j)
                self.assertEqual(a is None, b is None)
                if a is not None:
                    self.assertAlmostEqual(a, b, places=10)

    def test_retraction_order_independent(self):
        def build(order):
            pc = PairwiseCovariance(2)
            pc.add_batch("A", self.ROWS_A)
            pc.add_batch("B", self.ROWS_B)
            pc.add_batch("C", self.ROWS_C)
            for bid in order:
                pc.remove_batch(bid)
            return pc

        p1 = build(["A", "B"])
        p2 = build(["B", "A"])
        for i in range(2):
            for j in range(2):
                self.assertEqual(p1.count(i, j), p2.count(i, j))
                a, b = p1.covariance(i, j), p2.covariance(i, j)
                self.assertEqual(a is None, b is None)
                if a is not None:
                    self.assertAlmostEqual(a, b, places=10)

    def test_idempotent_batch_id(self):
        pc = PairwiseCovariance(2)
        self.assertTrue(pc.add_batch("A", self.ROWS_A))
        self.assertFalse(pc.add_batch("A", self.ROWS_A))  # no double count
        direct = PairwiseCovariance(2)
        direct.add_batch("once", self.ROWS_A)
        for i in range(2):
            for j in range(2):
                self.assertEqual(pc.count(i, j), direct.count(i, j))

    def test_unknown_batch_rejected(self):
        pc = PairwiseCovariance(2)
        pc.add_batch("A", self.ROWS_A)
        with self.assertRaises(KeyError):
            pc.remove_batch("does-not-exist")

    def test_counts_never_negative_after_full_retraction(self):
        pc = PairwiseCovariance(2)
        pc.add_batch("A", self.ROWS_A)
        pc.remove_batch("A")
        self.assertEqual(pc.count(0, 0), 0)
        self.assertEqual(pc.count(0, 1), 0)
        self.assertIsNone(pc.covariance(0, 0))
        with self.assertRaises(KeyError):
            pc.remove_batch("A")  # already gone

    def test_subtract_stats_negative_count_raises(self):
        with self.assertRaises(ValueError):
            subtract_stats(PairStats(count=1), PairStats(count=2))


class TestNumericalStability(unittest.TestCase):
    def test_huge_mean_tiny_variance(self):
        # mean 1e12, true variance 1: naive E[x^2]-E[x]^2 catastrophically
        # cancels (returns 0 or garbage in float64); Welford must not.
        base = 1e12
        xs = [base + k for k in range(10)]
        ys = [base - k for k in range(10)]
        rows = [[x, y] for x, y in zip(xs, ys)]
        pc = PairwiseCovariance(2)
        pc.add_batch("big", rows)

        exact_var_x = reference_covariance(rows, 0, 0)
        exact_var_y = reference_covariance(rows, 1, 1)
        exact_cov = reference_covariance(rows, 0, 1)
        self.assertAlmostEqual(pc.covariance(0, 0), float(exact_var_x), places=6)
        self.assertAlmostEqual(pc.covariance(1, 1), float(exact_var_y), places=6)
        self.assertAlmostEqual(pc.covariance(0, 1), float(exact_cov), places=6)
        self.assertAlmostEqual(pc.covariance(0, 0), 9.166666666666666, places=6)
        self.assertAlmostEqual(pc.covariance(0, 1), -9.166666666666666, places=6)

    def test_naive_sum_of_squares_would_fail(self):
        # Document the failure mode we avoid: raw-moment formula in float64.
        base = 1e12
        xs = [base + k for k in range(10)]
        n = len(xs)
        s1 = sum(xs)
        s2 = sum(x * x for x in xs)
        naive_var = (s2 - s1 * s1 / n) / (n - 1)
        self.assertNotAlmostEqual(naive_var, 9.166666666666666, places=6)


class TestPSDDiagnostics(unittest.TestCase):
    def test_pairwise_missingness_can_break_psd(self):
        # Each pair is perfectly observed on a *different* subset of rows:
        # corr(x,y)=+1, corr(x,z)=+1, corr(y,z)=-1 -> matrix not PSD.
        rows = [
            [0.0, 0.0, None],
            [1.0, 1.0, None],
            [2.0, 2.0, None],
            [0.0, None, 0.0],
            [1.0, None, 1.0],
            [2.0, None, 2.0],
            [None, 0.0, 2.0],
            [None, 1.0, 1.0],
            [None, 2.0, 0.0],
        ]
        pc = PairwiseCovariance(3)
        pc.add_batch("b", rows)
        diag = pc.check_psd()
        self.assertEqual(diag.status, "not_psd")
        self.assertIsNotNone(diag.counterexample)
        v = diag.counterexample
        dense = pc.matrix()
        self.assertLess(quadratic_form(dense, v), 0.0)
        self.assertLess(diag.min_eigenvalue, 0.0)

    def test_psd_matrix_passes(self):
        rows = [[float(i), 2.0 * i, -float(i)] for i in range(8)]
        pc = PairwiseCovariance(3)
        pc.add_batch("b", rows)
        diag = pc.check_psd()
        self.assertEqual(diag.status, "psd")
        self.assertIsNone(diag.counterexample)

    def test_undefined_pairs_reported_not_hidden(self):
        rows = [[1.0, None], [2.0, None]]
        pc = PairwiseCovariance(2)
        pc.add_batch("b", rows)
        diag = pc.check_psd()
        self.assertEqual(diag.status, "undefined_pairs")
        self.assertIn((0, 1), diag.undefined_pairs)
        # The matrix itself is untouched: still has the None hole.
        self.assertIsNone(pc.matrix()[0][1])


class TestExplicitShapeAndIndex(unittest.TestCase):
    def test_wrong_row_width_rejected(self):
        pc = PairwiseCovariance(3)
        with self.assertRaises(ValueError):
            pc.add_batch("bad", [[1.0, 2.0]])

    def test_index_out_of_range_rejected(self):
        pc = PairwiseCovariance(2)
        pc.add_batch("b", [[1.0, 2.0], [3.0, 4.0]])
        with self.assertRaises(IndexError):
            pc.covariance(0, 2)
        with self.assertRaises(IndexError):
            pc.count(-1, 0)

    def test_symmetric_indexing(self):
        pc = PairwiseCovariance(2)
        pc.add_batch("b", [[1.0, 2.0], [3.0, 5.0]])
        self.assertEqual(pc.covariance(0, 1), pc.covariance(1, 0))
        self.assertEqual(pc.count(1, 0), pc.count(0, 1))

    def test_dimension_must_be_explicit_positive(self):
        with self.assertRaises(ValueError):
            PairwiseCovariance(0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
