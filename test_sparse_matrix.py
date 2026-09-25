"""Unit tests for sparse_matrix (stdlib unittest only)."""

import unittest

from sparse_matrix import (
    DenseSnapshot,
    QuotaExceededError,
    ShapeMismatchError,
    SparseMatrix,
)


class DenseModel:
    """Hand-written dense reference implementation."""

    def __init__(self, nrows, ncols):
        self.grid = [[0] * ncols for _ in range(nrows)]

    def assign(self, rows, cols, values):
        # values must already be materialized (snapshot semantics)
        for i, row in enumerate(rows):
            for j, col in enumerate(cols):
                self.grid[row][col] = values[i][j]


class TestCanonicalFormat(unittest.TestCase):
    def test_indices_sorted_after_unordered_assignment(self):
        mat = SparseMatrix(3, 5)
        mat[[1], [4, 0, 2]] = [[7, 8, 9]]
        self.assertTrue(mat.is_canonical())
        self.assertEqual(mat[1, :].tolist(), [[8, 0, 9, 0, 7]])

    def test_explicit_zeros_are_stored_and_preserved(self):
        mat = SparseMatrix(2, 3)
        mat[0, 1] = [[0]]  # explicit zero
        self.assertEqual(mat.nnz, 1)
        self.assertEqual(mat.explicit_zero_count(), 1)
        mat[1, 0] = [[5]]  # unrelated update must not prune the zero
        self.assertEqual(mat.explicit_zero_count(), 1)
        self.assertEqual(mat.to_dense(), [[0, 0, 0], [5, 0, 0]])
        self.assertTrue(mat.is_canonical())

    def test_constructor_entries_canonicalized_last_wins(self):
        mat = SparseMatrix(2, 3, entries=[(0, 2, 1), (0, 0, 2), (0, 2, 9)])
        self.assertTrue(mat.is_canonical())
        self.assertEqual(mat.to_dense(), [[2, 0, 9], [0, 0, 0]])


class TestSliceAssignment(unittest.TestCase):
    def test_noncontiguous_indices(self):
        mat = SparseMatrix(4, 4)
        mat[[3, 0], [2, 0]] = [[1, 2], [3, 4]]
        self.assertEqual(mat.to_dense(), [
            [4, 0, 3, 0],
            [0, 0, 0, 0],
            [0, 0, 0, 0],
            [2, 0, 1, 0],
        ])

    def test_duplicate_targets_last_write_wins(self):
        mat = SparseMatrix(2, 2)
        mat[[0, 0], [1, 1]] = [[1, 2], [3, 4]]
        self.assertEqual(mat[0, 1].tolist(), [[4]])

    def test_scalar_broadcast(self):
        mat = SparseMatrix(3, 3)
        mat[[0, 2], [0, 1, 2]] = 7
        self.assertEqual(mat.to_dense(), [
            [7, 7, 7],
            [0, 0, 0],
            [7, 7, 7],
        ])

    def test_negative_indices_wrap(self):
        mat = SparseMatrix(2, 2)
        mat[-1, -1] = [[5]]
        self.assertEqual(mat.to_dense(), [[0, 0], [0, 5]])

    def test_overlapping_assignment_reads_pre_assignment_snapshot(self):
        mat = SparseMatrix(1, 3)
        mat[0, :] = [[1, 2, 3]]
        mat[[0], [1, 2]] = mat[[0], [0, 1]]  # shift right
        self.assertEqual(mat.to_dense(), [[1, 1, 2]])

    def test_overlap_2d_rotation(self):
        mat = SparseMatrix(2, 2)
        mat[:, :] = [[1, 2], [3, 4]]
        mat[[0, 1], [0, 1]] = mat[[0, 1], [1, 0]]  # swap columns
        self.assertEqual(mat.to_dense(), [[2, 1], [4, 3]])


class TestAtomicFailures(unittest.TestCase):
    def test_out_of_bounds_rejected_before_write(self):
        mat = SparseMatrix(2, 2)
        mat[0, 0] = [[1]]
        before = mat.to_dense()
        versions = mat.version_count
        with self.assertRaises(IndexError):
            mat[[0, 1], [0, 2]] = [[5, 6], [7, 8]]  # col 2 out of bounds
        self.assertEqual(mat.to_dense(), before)
        self.assertEqual(mat.version_count, versions)

    def test_shape_mismatch_leaves_matrix_unchanged(self):
        mat = SparseMatrix(2, 2)
        mat[0, 0] = [[1]]
        before = mat.to_dense()
        with self.assertRaises(ShapeMismatchError):
            mat[[0, 1], [0, 1]] = [[1, 2]]  # 1x2 values for 2x2 target
        self.assertEqual(mat.to_dense(), before)

    def test_quota_exceeded_no_partial_write(self):
        mat = SparseMatrix(3, 3, memory_quota=1)
        mat[0, 0] = [[1]]
        before = mat.to_dense()
        versions = mat.version_count
        total_before = mat.rows_copied_total
        with self.assertRaises(QuotaExceededError):
            mat[[0, 1], [0, 1]] = [[1, 2], [3, 4]]  # touches 2 row blocks
        self.assertEqual(mat.to_dense(), before)
        self.assertEqual(mat.version_count, versions)
        self.assertEqual(mat.rows_copied_total, total_before)

    def test_quota_exact_fit_succeeds(self):
        mat = SparseMatrix(3, 3, memory_quota=2)
        mat[[0, 2], [1]] = [[8], [9]]  # exactly 2 row blocks
        self.assertEqual(mat.rows_copied_last, 2)
        self.assertEqual(mat[0, 1].tolist(), [[8]])
        self.assertEqual(mat[2, 1].tolist(), [[9]])


class TestRowBlockStats(unittest.TestCase):
    def test_counts_distinct_rows_actually_copied(self):
        mat = SparseMatrix(4, 4)
        mat[[0, 0, 2], [1, 3, 0]] = 1  # rows {0, 2}
        self.assertEqual(mat.rows_copied_last, 2)
        mat[1, 1] = [[5]]
        self.assertEqual(mat.rows_copied_last, 1)
        self.assertEqual(mat.rows_copied_total, 3)


class TestHistoryViews(unittest.TestCase):
    def test_history_records_committed_versions(self):
        mat = SparseMatrix(2, 2)
        mat[0, 0] = [[1]]
        mat[1, 1] = [[2]]
        self.assertEqual(mat.version_count, 3)
        self.assertEqual(mat.history_view(0).to_dense(), [[0, 0], [0, 0]])
        self.assertEqual(mat.history_view(1).to_dense(), [[1, 0], [0, 0]])
        self.assertEqual(mat.history_view(-1).to_dense(), [[1, 0], [0, 2]])

    def test_history_view_is_read_only(self):
        mat = SparseMatrix(2, 2)
        view = mat.history_view(0)
        with self.assertRaises(TypeError):
            view[0, 0] = [[1]]
        with self.assertRaises(TypeError):
            view.assign([0], [0], [[1]])
        with self.assertRaises(TypeError):
            view.transaction()

    def test_history_views_immune_to_later_mutation(self):
        mat = SparseMatrix(2, 2)
        mat[0, 0] = [[1]]
        view = mat.history_view(1)
        mat[0, 0] = [[99]]
        mat[1, 1] = [[5]]
        self.assertEqual(view.to_dense(), [[1, 0], [0, 0]])

    def test_failed_assignment_adds_no_version(self):
        mat = SparseMatrix(2, 2, memory_quota=1)
        with self.assertRaises(QuotaExceededError):
            mat[[0, 1], [0]] = [[1], [2]]
        self.assertEqual(mat.version_count, 1)


class TestTransactions(unittest.TestCase):
    def test_transaction_rolls_back_on_failure(self):
        mat = SparseMatrix(3, 3)
        mat[0, 0] = [[1]]
        before = mat.to_dense()
        versions = mat.version_count
        with self.assertRaises(IndexError):
            with mat.transaction():
                mat[1, 1] = [[2]]
                mat[2, 3] = [[3]]  # out of bounds -> rollback
        self.assertEqual(mat.to_dense(), before)
        self.assertEqual(mat.version_count, versions)

    def test_transaction_commits_single_version(self):
        mat = SparseMatrix(3, 3)
        with mat.transaction():
            mat[0, 0] = [[1]]
            mat[1, 1] = [[2]]
            mat[2, 2] = [[3]]
        self.assertEqual(mat.version_count, 2)
        self.assertEqual(mat.to_dense(), [[1, 0, 0], [0, 2, 0], [0, 0, 3]])
        self.assertEqual(mat.history_view(0).to_dense(),
                         [[0, 0, 0], [0, 0, 0], [0, 0, 0]])

    def test_transaction_restores_stats_on_rollback(self):
        mat = SparseMatrix(2, 2)
        mat[0, 0] = [[1]]
        stats = (mat.rows_copied_last, mat.rows_copied_total)
        with self.assertRaises(IndexError):
            with mat.transaction():
                mat[1, 1] = [[2]]
                mat[0, 5] = [[3]]
        self.assertEqual((mat.rows_copied_last, mat.rows_copied_total), stats)


class TestRandomizedDenseComparison(unittest.TestCase):
    def test_random_overlapping_assignments_match_dense_model(self):
        import random

        rng = random.Random(20260925)
        for trial in range(300):
            nrows = rng.randint(1, 6)
            ncols = rng.randint(1, 6)
            mat = SparseMatrix(nrows, ncols)
            model = DenseModel(nrows, ncols)
            for _ in range(rng.randint(1, 15)):
                rows = [rng.randrange(nrows) for _ in range(rng.randint(1, 4))]
                cols = [rng.randrange(ncols) for _ in range(rng.randint(1, 4))]
                pick = rng.random()
                if pick < 0.4:
                    # Overlapping read from the matrix itself: the RHS is a
                    # materialized snapshot taken before the assignment.
                    src_rows = [rng.randrange(nrows) for _ in rows]
                    src_cols = [rng.randrange(ncols) for _ in cols]
                    values = mat[src_rows, src_cols]
                    dense_values = [[model.grid[r][c] for c in src_cols]
                                    for r in src_rows]
                elif pick < 0.7:
                    values = rng.randint(-5, 5)  # scalar broadcast
                    dense_values = [[values] * len(cols) for _ in rows]
                else:
                    values = [[rng.randint(-5, 5) for _ in cols]
                              for _ in rows]
                    dense_values = values
                mat[rows, cols] = values
                model.assign(rows, cols, dense_values)
                self.assertEqual(mat.to_dense(), model.grid,
                                 f"trial {trial}")
                self.assertTrue(mat.is_canonical(), f"trial {trial}")


class TestDenseSnapshot(unittest.TestCase):
    def test_snapshot_is_immutable_copy(self):
        mat = SparseMatrix(2, 2)
        mat[0, 0] = [[1]]
        snap = mat[:, :]
        self.assertIsInstance(snap, DenseSnapshot)
        mat[0, 0] = [[9]]
        self.assertEqual(snap.tolist(), [[1, 0], [0, 0]])
        self.assertEqual(snap.shape, (2, 2))


if __name__ == "__main__":
    unittest.main()
