import unittest

from sparse_assign import (
    WITHDRAW,
    AssignmentStats,
    BadRhsError,
    BadSelectorError,
    DenseReference,
    IndexBoundaryError,
    MemoryQuota,
    QuotaExceededError,
    ReadOnlyError,
    SparseMatrix,
)


class ConstructionAndCanonical(unittest.TestCase):
    def test_explicit_zeros_are_retained(self):
        m = SparseMatrix(2, 2, [(0, 0, 0), (1, 1, 0.0)])
        self.assertEqual(m.stored_entries, 2)
        m.canonical_check()
        self.assertEqual(m.to_dense(), ((0, 0), (0, 0)))

    def test_duplicate_construction_entries_last_wins(self):
        m = SparseMatrix(1, 3, [(0, 0, 1), (0, 0, 2), (0, 0, 3)])
        self.assertEqual(m.stored_entries, 1)
        self.assertEqual(m[0, 0], 3)

    def test_construction_rejects_out_of_range_before_storing(self):
        with self.assertRaises(IndexError):
            SparseMatrix(2, 2, [(0, 0, 1), (5, 0, 2)])
        with self.assertRaises(BadRhsError):
            SparseMatrix(2, 2, [(0, 0, object())])


class IndexingAndSlices(unittest.TestCase):
    def setUp(self):
        self.m = SparseMatrix(
            4, 4, [(0, 0, 1), (1, 1, 2), (2, 2, 3), (3, 3, 4)]
        )

    def test_noncontiguous_fancy_rows_and_cols(self):
        # Equal-length fancy sequences pair element-wise (NumPy semantics).
        stats = self.m.assign([0, 2], [1, 3], 7)
        self.m.canonical_check()
        self.assertEqual(
            self.m.to_dense(),
            (
                (1, 7, 0, 0),
                (0, 2, 0, 0),
                (0, 0, 3, 7),
                (0, 0, 0, 4),
            ),
        )
        self.assertEqual(stats.cells_written, 2)
        self.assertEqual(stats.row_blocks_copied, 2)

    def test_cartesian_grid_with_a_slice_axis(self):
        stats = self.m.assign(slice(None, None, 2), [1, 3], 7)
        self.m.canonical_check()
        self.assertEqual(
            self.m.to_dense(),
            (
                (1, 7, 0, 7),
                (0, 2, 0, 0),
                (0, 7, 3, 7),
                (0, 0, 0, 4),
            ),
        )
        self.assertEqual(stats.cells_written, 4)
        self.assertEqual(stats.row_blocks_copied, 2)

    def test_negative_indices_and_slice_stepping(self):
        self.m.assign(slice(None, None, -1), [-1, -2], 8)
        self.m.canonical_check()
        for r in range(4):
            self.assertEqual(self.m[r, 3], 8)
            self.assertEqual(self.m[r, 2], 8)
        self.assertEqual(self.m[0, 0], 1)

    def test_slice_clips_at_boundary(self):
        self.m.assign(slice(2, 99), slice(0, 2), 5)
        self.m.canonical_check()
        self.assertEqual(
            self.m.to_dense(),
            (
                (1, 0, 0, 0),
                (0, 2, 0, 0),
                (5, 5, 3, 0),
                (5, 5, 0, 4),
            ),
        )

    def test_out_of_range_indices_rejected_before_write(self):
        before = self.m.to_dense()
        with self.assertRaises(IndexBoundaryError):
            self.m.assign(4, 0, 1)
        with self.assertRaises(IndexBoundaryError):
            self.m.assign([0, 4], 0, 1)
        with self.assertRaises(IndexBoundaryError):
            self.m.assign(0, [-5], 1)
        with self.assertRaises(IndexBoundaryError):
            self.m.assign(slice(0, 4), 4, 1)
        self.assertEqual(self.m.to_dense(), before)
        self.assertEqual(self.m.version, 0)

    def test_bad_selector_types_are_rejected(self):
        with self.assertRaises(BadSelectorError):
            self.m.assign(0.5, 0, 1)
        with self.assertRaises(BadSelectorError):
            self.m.assign([0, 1.0], 0, 1)
        with self.assertRaises(BadSelectorError):
            self.m.assign("0", 0, 1)

    def test_scalar_selection_returns_plain_value(self):
        self.assertEqual(self.m[1, 1], 2)
        self.assertEqual(self.m[0, 3], 0)
        self.assertEqual(self.m.get(0, 3, default=None), None)

    def test_region_selection_reads_missing_as_zero(self):
        sel = self.m.select([0, 2], [0, 1, 2])
        self.assertEqual(sel.shape, (2, 3))
        self.assertEqual(sel.flatten(), [1, 0, 0, 0, 0, 3])


class RepeatsBroadcastWithdraw(unittest.TestCase):
    def test_repeated_target_last_write_wins_cartesian(self):
        m = SparseMatrix(1, 4)
        stats = m.assign([0], [0, 0, 1, 1], [10, 20, 30, 40])
        self.assertEqual([m[0, 0], m[0, 1]], [20, 40])
        self.assertEqual(stats.repeated_targets, 2)
        self.assertEqual(stats.cells_written, 2)

    def test_paired_fancy_indices(self):
        m = SparseMatrix(3, 3)
        m.assign([0, 1, 2], [2, 1, 0], [5, 6, 7])
        self.assertEqual(
            m.to_dense(),
            (
                (0, 0, 5),
                (0, 6, 0),
                (7, 0, 0),
            ),
        )

    def test_paired_length_mismatch_falls_back_to_grid_check(self):
        m = SparseMatrix(3, 3)
        with self.assertRaises(Exception):
            m.assign([0, 1], [0, 1, 2], [[1, 2, 3], [4, 5, 6], [7, 8, 9]])

    def test_broadcast_rows_and_columns(self):
        m = SparseMatrix(3, 3)
        # slices force the Cartesian grid so broadcast rules are visible.
        m.assign(slice(None), slice(None), [[9], [8], [7]])
        self.assertEqual(
            m.to_dense(),
            ((9, 9, 9), (8, 8, 8), (7, 7, 7)),
        )
        m.assign(slice(None), slice(None), [2, 3, 4])
        self.assertEqual(
            m.to_dense(),
            ((2, 3, 4), (2, 3, 4), (2, 3, 4)),
        )
        m.assign(slice(None), slice(None), 0)
        self.assertEqual(m.stored_entries, 9)

    def test_shape_mismatch_and_ragged_rhs(self):
        m = SparseMatrix(3, 3)
        with self.assertRaises(Exception):
            m.assign([0, 1], [0, 1], [1, 2, 3])
        with self.assertRaises(Exception):
            m.assign(None, None, [[1, 2], [3]])
        with self.assertRaises(BadRhsError):
            m.assign(0, 0, object())

    def test_withdraw_removes_cell_but_zero_stays_explicit(self):
        m = SparseMatrix(2, 2, [(0, 0, 5), (0, 1, 5)])
        m.assign(0, 0, WITHDRAW)
        self.assertEqual(m.stored_entries, 1)
        m.assign(0, 1, 0)
        self.assertEqual(m.stored_entries, 1)
        m.assign(0, 0, 0)
        self.assertEqual(m.stored_entries, 2)
        m.assign(0, 0, WITHDRAW)
        self.assertEqual(m.stored_entries, 1)
        m.canonical_check()

    def test_withdraw_absent_is_unchanged(self):
        m = SparseMatrix(1, 1)
        stats = m.assign(0, 0, WITHDRAW)
        self.assertEqual(stats.cells_withdrawn, 0)
        self.assertEqual(stats.cells_unchanged, 1)
        self.assertEqual(stats.row_blocks_copied, 0)


class OverlapAndSnapshot(unittest.TestCase):
    def test_self_overlap_reads_pre_assignment_snapshot(self):
        m = SparseMatrix(1, 4, [(0, 0, 1), (0, 1, 2), (0, 2, 3), (0, 3, 4)])
        m.assign([0], [0, 1, 2, 3], m.select([0], [0, 1, 2, 3]))  # identity
        self.assertEqual(m.to_dense(), ((1, 2, 3, 4),))
        m.assign(slice(0, 1), [1, 2, 3], m.select(slice(0, 1), [0, 1, 2]))
        self.assertEqual(m.to_dense(), ((1, 1, 2, 3),))

    def test_self_rhs_shorthand_is_also_pre_snapshot(self):
        m = SparseMatrix(1, 4, [(0, 0, 1), (0, 1, 2), (0, 2, 3), (0, 3, 4)])
        m.assign(slice(0, 1), [1, 2, 3], m)  # reads target region pre-state
        self.assertEqual(m.to_dense(), ((1, 2, 3, 4),))
        # a different overlapping source region is read pre-assignment
        m.assign(slice(0, 1), [1, 2, 3], m.select(slice(0, 1), [0, 1, 2]))
        self.assertEqual(m.to_dense(), ((1, 1, 2, 3),))

    def test_history_view_is_read_only_and_immutable(self):
        m = SparseMatrix(2, 2, [(0, 0, 1)])
        view = m.snapshot()
        self.assertEqual(view.version, 0)
        m.assign(None, None, 9)
        self.assertEqual(view[0, 0], 1)
        self.assertEqual(view[1, 1], 0)
        self.assertEqual(m[0, 0], 9)
        with self.assertRaises(ReadOnlyError):
            view.assign(0, 0, 1)

    def test_selection_pinned_to_historical_snapshot(self):
        m = SparseMatrix(1, 3, [(0, 0, 1), (0, 1, 2)])
        view = m.snapshot()
        pinned = view.select(0, [0, 1, 2])
        m.assign(0, [0, 1, 2], [7, 8, 9])
        self.assertEqual(pinned.flatten(), [1, 2, 0])

    def test_region_of_other_sparse_matrix_as_rhs(self):
        src = SparseMatrix(2, 2, [(0, 0, 11), (1, 1, 22)])
        dst = DenseReference(2, 2)
        dst.assign(None, None, src)
        self.assertEqual(dst.to_dense(), ((11, 0), (0, 22)))
        self.assertTrue(all(dst.presence_grid()[r][c] == (r == c)
                            for r in range(2) for c in range(2)))


class QuotaAndAtomicity(unittest.TestCase):
    def setUp(self):
        self.m = SparseMatrix(
            4, 4, [(r, r, r + 1) for r in range(4)]
        )

    def test_quota_rejects_before_any_partial_write(self):
        before = self.m.to_dense()
        version = self.m.version
        quota = MemoryQuota(1)
        with self.assertRaises(QuotaExceededError):
            self.m.assign([0, 1, 2], 0, 9, quota=quota)
        self.assertEqual(self.m.to_dense(), before)
        self.assertEqual(self.m.version, version)
        self.assertEqual(quota.used, 0)

    def test_quota_allows_within_budget_and_counts_real_copies(self):
        quota = MemoryQuota(4)
        stats = self.m.assign([0, 1], 0, 9, quota=quota)
        self.assertEqual(stats.row_blocks_copied, 2)
        self.assertEqual(quota.used, 2)
        stats2 = self.m.assign([0, 1], 0, 9, quota=quota)
        self.assertEqual(stats2.row_blocks_copied, 0)
        self.assertEqual(quota.used, 2)

    def test_failure_mid_validation_leaves_untouched(self):
        before = self.m.to_dense()
        with self.assertRaises(Exception):
            self.m.assign([0, 9], [0, 1], [[1, 2], [3, 4]])
        self.assertEqual(self.m.to_dense(), before)

    def test_empty_target_copies_nothing(self):
        quota = MemoryQuota(0)
        stats = self.m.assign(slice(0, 0), None, 9, quota=quota)
        self.assertEqual(stats.row_blocks_copied, 0)
        self.assertIsInstance(stats, AssignmentStats)


class EdgeSizes(unittest.TestCase):
    def test_empty_matrix(self):
        m = SparseMatrix(0, 0)
        stats = m.assign(slice(None), slice(None), 1)
        self.assertEqual(stats.row_blocks_copied, 0)
        m.canonical_check()

    def test_zero_columns_scalar_row_index_rejected(self):
        m = SparseMatrix(2, 0)
        with self.assertRaises(IndexBoundaryError):
            m.assign(0, 0, 1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
