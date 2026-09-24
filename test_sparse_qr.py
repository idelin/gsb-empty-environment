"""Unit tests for the sparse column-pivoted QR least-squares solver."""

import math
import random
import unittest

from dense_reference import dense_least_squares
from sparse_qr import SparseMatrix, qr_least_squares


def dense_entries(rows):
    return [(i, j, v) for i, row in enumerate(rows)
            for j, v in enumerate(row) if v != 0.0]


def random_sparse(rng, m, n, density):
    entries = []
    for i in range(m):
        for j in range(n):
            if rng.random() < density:
                entries.append((i, j, rng.uniform(-2.0, 2.0)))
    # Guarantee no all-zero column.
    for j in range(n):
        if not any(e[1] == j for e in entries):
            entries.append((rng.randrange(m), j, rng.uniform(0.5, 1.0)))
    return SparseMatrix(m, n, entries)


class TestOverdetermined(unittest.TestCase):
    def test_exact_consistent_system(self):
        rows = [[1.0, 0.0, 0.0], [1.0, 1.0, 1.0], [1.0, 2.0, 4.0],
                [1.0, 3.0, 9.0], [1.0, 4.0, 16.0]]
        x_true = [2.0, -1.0, 0.5]
        b = [sum(r * x for r, x in zip(row, x_true)) for row in rows]
        res = qr_least_squares(SparseMatrix(5, 3, dense_entries(rows)), b)
        self.assertEqual(res.rank, 3)
        self.assertFalse(res.rank_deficient)
        for got, want in zip(res.x, x_true):
            self.assertAlmostEqual(got, want, places=10)
        self.assertAlmostEqual(res.residual_norm, 0.0, places=10)

    def test_noisy_system_matches_dense_reference(self):
        rng = random.Random(42)
        A = random_sparse(rng, 12, 5, 0.6)
        b = [rng.uniform(-3, 3) for _ in range(12)]
        res = qr_least_squares(A, b)
        rows = [[A._cols[j].get(i, 0.0) for j in range(5)] for i in range(12)]
        xd, rank_d, rnorm_d, _ = dense_least_squares(rows, b)
        self.assertEqual(res.rank, rank_d)
        self.assertAlmostEqual(res.residual_norm, rnorm_d, places=9)
        for got, want in zip(res.x, xd):
            self.assertAlmostEqual(got, want, places=8)

    def test_residual_orthogonal_to_columns(self):
        rng = random.Random(7)
        A = random_sparse(rng, 10, 4, 0.7)
        b = [rng.uniform(-1, 1) for _ in range(10)]
        res = qr_least_squares(A, b)
        self.assertEqual(res.rank, 4)
        for j in range(4):
            dot = sum(A._cols[j].get(i, 0.0) * res.residual[i]
                      for i in range(10))
            self.assertAlmostEqual(dot, 0.0, places=8)


class TestRankDeficiency(unittest.TestCase):
    def test_duplicate_columns_reported(self):
        rows = [[1.0, 4.0, 1.0], [2.0, 3.0, 2.0],
                [3.0, 2.0, 3.0], [4.0, 1.0, 4.0]]
        A = SparseMatrix(4, 3, dense_entries(rows))
        b = [1.0, 1.0, 1.0, 1.0]
        res = qr_least_squares(A, b)
        self.assertEqual(res.rank, 2)
        self.assertTrue(res.rank_deficient)
        self.assertEqual(res.dependent_columns, [2])
        # Still a valid least-squares solution: residual matches reference.
        _, _, rnorm_d, def_d = dense_least_squares(rows, b)
        self.assertTrue(def_d)
        self.assertAlmostEqual(res.residual_norm, rnorm_d, places=9)

    def test_triplicated_column(self):
        rows = [[1.0, 2.0, 1.0, 1.0], [0.0, 1.0, 0.0, 0.0],
                [1.0, 0.0, 1.0, 1.0], [1.0, 1.0, 1.0, 1.0]]
        res = qr_least_squares(SparseMatrix(4, 4, dense_entries(rows)),
                               [1.0, 2.0, 3.0, 4.0])
        self.assertEqual(res.rank, 2)
        self.assertTrue(res.rank_deficient)
        self.assertEqual(len(res.dependent_columns), 2)

    def test_near_dependent_columns(self):
        # Column 2 = column 0 + column 1 + 1e-16 * noise.
        rng = random.Random(3)
        c0 = [rng.uniform(-1, 1) for _ in range(6)]
        c1 = [rng.uniform(-1, 1) for _ in range(6)]
        c2 = [a + b + 1e-16 * rng.uniform(-1, 1) for a, b in zip(c0, c1)]
        rows = [list(t) for t in zip(c0, c1, c2)]
        res = qr_least_squares(SparseMatrix(6, 3, dense_entries(rows)),
                               [rng.uniform(-1, 1) for _ in range(6)])
        self.assertEqual(res.rank, 2)
        self.assertTrue(res.rank_deficient)

    def test_zero_matrix(self):
        res = qr_least_squares(SparseMatrix(3, 2, []), [1.0, 2.0, 3.0])
        self.assertEqual(res.rank, 0)
        self.assertTrue(res.rank_deficient)
        self.assertEqual(res.x, [0.0, 0.0])
        self.assertAlmostEqual(res.residual_norm, math.sqrt(14.0))


class TestPermutations(unittest.TestCase):
    def test_column_pivot_order_restored(self):
        # Disparate column norms force a nontrivial pivot order.
        rows = [[1e-4, 1.0, 0.0], [0.0, 1.0, 1.0], [1e-4, 0.0, 1.0]]
        b = [1.0, 2.0, 3.0]
        A = SparseMatrix(3, 3, dense_entries(rows))
        res = qr_least_squares(A, b)
        self.assertNotEqual(res.column_permutation, [0, 1, 2])
        # x is in the ORIGINAL variable order: A @ x == b.
        for ai, bi in zip(A.matvec(res.x), b):
            self.assertAlmostEqual(ai, bi, places=8)
        xd, _, _, _ = dense_least_squares(rows, b)
        for got, want in zip(res.x, xd):
            self.assertAlmostEqual(got, want, places=8)

    def test_row_permutation_invariance(self):
        rng = random.Random(11)
        rows = [[rng.uniform(-2, 2) for _ in range(4)] for _ in range(9)]
        b = [rng.uniform(-2, 2) for _ in range(9)]
        order = list(range(9))
        rng.shuffle(order)
        rows_p = [rows[i] for i in order]
        b_p = [b[i] for i in order]
        r1 = qr_least_squares(SparseMatrix(9, 4, dense_entries(rows)), b)
        r2 = qr_least_squares(SparseMatrix(9, 4, dense_entries(rows_p)), b_p)
        self.assertEqual(r1.rank, r2.rank)
        self.assertAlmostEqual(r1.residual_norm, r2.residual_norm, places=9)
        for got, want in zip(r1.x, r2.x):
            self.assertAlmostEqual(got, want, places=8)


class TestGeneral(unittest.TestCase):
    def test_underdetermined_system(self):
        rng = random.Random(5)
        A = random_sparse(rng, 3, 6, 0.8)
        b = [rng.uniform(-1, 1) for _ in range(3)]
        res = qr_least_squares(A, b)
        self.assertEqual(res.rank, 3)
        for ai, bi in zip(A.matvec(res.x), b):
            self.assertAlmostEqual(ai, bi, places=8)

    def test_random_problems_against_dense_reference(self):
        rng = random.Random(2024)
        for trial in range(30):
            m = rng.randint(2, 10)
            n = rng.randint(2, 8)
            A = random_sparse(rng, m, n, rng.uniform(0.3, 0.9))
            b = [rng.uniform(-2, 2) for _ in range(m)]
            rows = [[A._cols[j].get(i, 0.0) for j in range(n)]
                    for i in range(m)]
            res = qr_least_squares(A, b)
            xd, rank_d, rnorm_d, def_d = dense_least_squares(rows, b)
            self.assertEqual(res.rank, rank_d, msg=f"trial {trial}")
            self.assertEqual(res.rank_deficient, def_d)
            self.assertAlmostEqual(res.residual_norm, rnorm_d, places=7)
            if res.rank == n:  # unique solution -> x must match too
                for got, want in zip(res.x, xd):
                    self.assertAlmostEqual(got, want, places=6,
                                           msg=f"trial {trial}")

    def test_duplicate_entries_summed_and_validation(self):
        A = SparseMatrix(2, 2, [(0, 0, 1.0), (0, 0, 1.0), (1, 1, 3.0)])
        self.assertEqual(A._cols[0], {0: 2.0})
        with self.assertRaises(IndexError):
            SparseMatrix(2, 2, [(2, 0, 1.0)])
        with self.assertRaises(ValueError):
            SparseMatrix(0, 2, [])
        with self.assertRaises(ValueError):
            qr_least_squares(A, [1.0])


if __name__ == "__main__":
    unittest.main(verbosity=2)
