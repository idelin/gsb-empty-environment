import math
import random
import unittest

from sparseqr import SparseMatrix, qr_least_squares
from sparseqr.dense import dense_qr_least_squares


def norm(vec):
    return math.sqrt(sum(v * v for v in vec))


def dense_from(matrix):
    return matrix.to_dense()


class SparseMatrixInputTests(unittest.TestCase):
    def test_explicit_sizes_and_indices(self):
        a = SparseMatrix(3, 2, [(0, 0, 1.0), (2, 1, 4.0), (1, 0, 2.0)])
        self.assertEqual(a.shape, (3, 2))
        self.assertEqual(a.nnz, 3)
        self.assertEqual(a.to_dense(), [[1.0, 0.0], [2.0, 0.0], [0.0, 4.0]])

    def test_duplicate_entries_are_summed(self):
        a = SparseMatrix(2, 2, [(0, 0, 1.0), (0, 0, 2.5), (1, 1, -1.0)])
        self.assertEqual(a.to_dense(), [[3.5, 0.0], [0.0, -1.0]])

    def test_out_of_range_index_raises(self):
        with self.assertRaises(IndexError):
            SparseMatrix(2, 2, [(2, 0, 1.0)])
        with self.assertRaises(IndexError):
            SparseMatrix(2, 2, [(0, -1, 1.0)])

    def test_rhs_length_mismatch_raises(self):
        a = SparseMatrix(3, 2, [(0, 0, 1.0)])
        with self.assertRaises(ValueError):
            qr_least_squares(a, [1.0, 2.0])


class LeastSquaresTests(unittest.TestCase):
    def test_overdetermined_line_fit(self):
        # Fit y = c0 + c1 * t through exact data; residual must vanish.
        ts = [0.0, 1.0, 2.0, 3.0, 4.0, 5.0]
        c0, c1 = 1.5, -2.0
        entries = [(i, 0, 1.0) for i in range(len(ts))]
        entries += [(i, 1, t) for i, t in enumerate(ts)]
        b = [c0 + c1 * t for t in ts]
        res = qr_least_squares(SparseMatrix(len(ts), 2, entries), b)
        self.assertEqual(res.rank, 2)
        self.assertFalse(res.rank_deficient)
        self.assertAlmostEqual(res.x[0], c0, places=10)
        self.assertAlmostEqual(res.x[1], c1, places=10)
        self.assertAlmostEqual(res.residual_norm, 0.0, places=10)

    def test_overdetermined_inconsistent_has_residual(self):
        # Best approximation of b in span of a single column of ones.
        entries = [(i, 0, 1.0) for i in range(4)]
        b = [1.0, 2.0, 3.0, 10.0]
        res = qr_least_squares(SparseMatrix(4, 1, entries), b)
        self.assertEqual(res.rank, 1)
        self.assertAlmostEqual(res.x[0], 4.0, places=12)  # mean of b
        self.assertAlmostEqual(res.residual_norm, norm([v - 4.0 for v in b]), places=12)
        self.assertEqual(len(res.residuals), 4)

    def test_square_exact_system(self):
        entries = [(0, 0, 4.0), (0, 1, 1.0), (1, 0, 2.0), (1, 1, 3.0)]
        b = [1.0, 2.0]
        res = qr_least_squares(SparseMatrix(2, 2, entries), b)
        # Exact solution: x0 = 0.1, x1 = 0.6
        self.assertAlmostEqual(res.x[0], 0.1, places=12)
        self.assertAlmostEqual(res.x[1], 0.6, places=12)
        self.assertAlmostEqual(res.residual_norm, 0.0, places=12)

    def test_duplicate_columns_report_rank_deficiency(self):
        # Column 2 is an exact duplicate of column 0.
        entries = [
            (0, 0, 1.0), (1, 0, 2.0), (2, 0, 3.0),
            (0, 1, 0.5), (1, 1, 1.0), (2, 1, -1.0),
            (0, 2, 1.0), (1, 2, 2.0), (2, 2, 3.0),
        ]
        # b = col0 + col1 lies exactly in the rank-2 column span.
        b = [1.5, 3.0, 2.0]
        res = qr_least_squares(SparseMatrix(3, 3, entries), b)
        self.assertEqual(res.rank, 2)
        self.assertTrue(res.rank_deficient)
        self.assertEqual(res.dependent_columns, [2])
        # The basic solution zeroes the duplicated column.
        self.assertEqual(res.x[2], 0.0)
        self.assertLess(res.residual_norm, 1e-10)

    def test_scaled_duplicate_column_detected(self):
        # Column 1 = 2 * column 0 -> rank 1, reported as dependent.
        entries = [
            (0, 0, 1.0), (1, 0, -1.0), (2, 0, 2.0),
            (0, 1, 2.0), (1, 1, -2.0), (2, 1, 4.0),
        ]
        res = qr_least_squares(SparseMatrix(3, 2, entries), [1.0, 1.0, 1.0])
        self.assertEqual(res.rank, 1)
        self.assertTrue(res.rank_deficient)
        # Column 1 has the larger norm, so pivoting keeps it and reports
        # column 0 as the redundant one.
        self.assertEqual(res.dependent_columns, [0])

    def test_near_collinear_columns_use_pivoting(self):
        # Column 1 is column 0 plus a tiny perturbation; the solver must
        # not produce huge cancelling coefficients.
        eps = 1e-9
        entries = [
            (0, 0, 1.0), (1, 0, 2.0), (2, 0, 3.0), (3, 0, 4.0),
            (0, 1, 1.0 + eps), (1, 1, 2.0), (2, 1, 3.0), (3, 1, 4.0),
            (0, 2, 2.0), (1, 2, 1.0), (2, 2, 0.0), (3, 2, 1.0),
        ]
        b = [1.0, 2.0, 3.0, 4.0]
        res = qr_least_squares(SparseMatrix(4, 3, entries), b, tol=1e-6)
        self.assertEqual(res.rank, 2)
        self.assertTrue(res.rank_deficient)
        self.assertLess(norm(res.x), 1e3)
        # Compare against the dense reference: same minimal residual.
        _, rank_d, res_d, _ = dense_qr_least_squares(
            SparseMatrix(4, 3, entries).to_dense(), b, tol=1e-6
        )
        self.assertEqual(rank_d, 2)
        self.assertAlmostEqual(res.residual_norm, res_d, places=8)

    def test_column_permutation_restored(self):
        # First column has tiny norm so pivoting must move it last; the
        # returned x must still be in the original variable order.
        entries = [
            (0, 0, 1e-3),
            (0, 1, 1.0), (1, 1, 1.0),
            (0, 2, 0.0), (1, 2, 1.0), (2, 2, 1.0),
        ]
        a = SparseMatrix(3, 3, entries)
        b = [1.0, 2.0, 3.0]
        res = qr_least_squares(a, b)
        self.assertNotEqual(res.column_permutation, [0, 1, 2])
        # Verify x in original order actually solves the system.
        ax = a.matvec(res.x)
        for got, want in zip(ax, b):
            self.assertAlmostEqual(got, want, places=9)
        # Cross-check against dense reference solution (MGS is less
        # stable than Householder on this scaled problem, hence places=7).
        x_d, _, _, _ = dense_qr_least_squares(a.to_dense(), b)
        for got, want in zip(res.x, x_d):
            self.assertAlmostEqual(got, want, places=7)

    def test_underdetermined_basic_solution(self):
        entries = [(0, 0, 1.0), (1, 1, 1.0), (0, 2, 1.0), (1, 2, 1.0)]
        a = SparseMatrix(2, 3, entries)
        b = [3.0, 4.0]
        res = qr_least_squares(a, b)
        self.assertEqual(res.rank, 2)
        ax = a.matvec(res.x)
        for got, want in zip(ax, b):
            self.assertAlmostEqual(got, want, places=12)

    def test_zero_matrix(self):
        res = qr_least_squares(SparseMatrix(3, 2, []), [1.0, 2.0, 3.0])
        self.assertEqual(res.rank, 0)
        self.assertTrue(res.rank_deficient)
        self.assertEqual(res.x, [0.0, 0.0])
        self.assertAlmostEqual(res.residual_norm, norm([1.0, 2.0, 3.0]))

    def test_zero_column_is_reported_dependent(self):
        entries = [(0, 0, 1.0), (1, 0, 2.0)]
        res = qr_least_squares(SparseMatrix(2, 2, entries), [1.0, 1.0])
        self.assertEqual(res.rank, 1)
        self.assertEqual(res.dependent_columns, [1])
        self.assertEqual(res.x[1], 0.0)


class RandomizedCrossCheckTests(unittest.TestCase):
    def random_sparse(self, rng, m, n, density):
        entries = []
        for j in range(n):
            for i in range(m):
                if rng.random() < density:
                    entries.append((i, j, rng.uniform(-2.0, 2.0)))
        return SparseMatrix(m, n, entries)

    def test_matches_dense_reference_full_rank(self):
        rng = random.Random(20260924)
        for _ in range(25):
            m = rng.randint(6, 12)
            n = rng.randint(2, 5)
            a = self.random_sparse(rng, m, n, density=0.5)
            b = [rng.uniform(-3.0, 3.0) for _ in range(m)]
            res = qr_least_squares(a, b)
            x_d, rank_d, res_d, _ = dense_qr_least_squares(a.to_dense(), b)
            self.assertEqual(res.rank, rank_d)
            self.assertAlmostEqual(res.residual_norm, res_d, places=8)
            if res.rank == n:
                for got, want in zip(res.x, x_d):
                    self.assertAlmostEqual(got, want, places=7)

    def test_matches_dense_reference_rank_deficient(self):
        rng = random.Random(7)
        for _ in range(25):
            m = rng.randint(6, 10)
            base = self.random_sparse(rng, m, 2, density=0.7)
            # Append a duplicate and a scaled copy of existing columns.
            entries = []
            dense = base.to_dense()
            for j in range(2):
                for i in range(m):
                    if dense[i][j] != 0.0:
                        entries.append((i, j, dense[i][j]))
            for i in range(m):
                if dense[i][0] != 0.0:
                    entries.append((i, 2, dense[i][0]))          # duplicate
                if dense[i][1] != 0.0:
                    entries.append((i, 3, -3.0 * dense[i][1]))   # scaled copy
            a = SparseMatrix(m, 4, entries)
            b = [rng.uniform(-1.0, 1.0) for _ in range(m)]
            res = qr_least_squares(a, b)
            _, rank_d, res_d, _ = dense_qr_least_squares(a.to_dense(), b)
            self.assertEqual(res.rank, rank_d)
            self.assertTrue(res.rank_deficient)
            # Exactly one of each dependent pair {0, 2} and {1, 3} must
            # be reported as redundant.
            dependent = set(res.dependent_columns)
            self.assertEqual(len(dependent), 2)
            self.assertEqual(len(dependent & {0, 2}), 1)
            self.assertEqual(len(dependent & {1, 3}), 1)
            self.assertAlmostEqual(res.residual_norm, res_d, places=8)


if __name__ == "__main__":
    unittest.main()
