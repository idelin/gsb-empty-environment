"""Differential checks: the sparse matrix vs. the dense snapshot model.

Every random operation is applied to both models in lock-step.  Overlapping
matrix sources are pinned to independent selections *before* either model is
mutated, so the pre-assignment snapshot rule is exercised directly.
"""

import random
import unittest

from sparse_assign import (
    ABSENT,
    WITHDRAW,
    DenseReference,
    IndexBoundaryError,
    MemoryQuota,
    QuotaExceededError,
    ShapeMismatchError,
    SparseMatrix,
)

NROWS = 6
NCOLS = 5
SEED = 20260925
TRIALS = 500


def random_selector(rng: random.Random, n: int):
    kind = rng.randrange(6)
    if kind == 0:
        return rng.randrange(-n, n + 1)  # sometimes out of range
    if kind == 1:
        return slice(
            rng.randrange(-n, n + 1),
            rng.randrange(-n, n + 1),
            rng.choice([1, 1, 1, 2, -1]),
        )
    if kind == 2:
        return slice(None, None, rng.choice([1, 1, 2, -1]))
    if kind == 3:
        return slice(rng.randrange(-n, n + 1), None)
    length = rng.randrange(0, n + 2)
    return [rng.randrange(-n, n + 1) for _ in range(length)]


def sparse_presence(sparse, nrows=NROWS, ncols=NCOLS):
    return tuple(
        tuple(sparse.read_cell(r, c) is not ABSENT for c in range(ncols))
        for r in range(nrows)
    )


class DifferentialTests(unittest.TestCase):
    def setUp(self):
        self.rng = random.Random(SEED)

    def _make_pair(self):
        rng = self.rng
        entries = [
            (r, c, rng.choice([0, 1, 2, 3, 5, 8, 0.0]))
            for r in range(NROWS)
            for c in range(NCOLS)
            if rng.random() < 0.35
        ]
        return (
            SparseMatrix(NROWS, NCOLS, entries),
            DenseReference(NROWS, NCOLS, entries),
        )

    def _rhs(self, sparse, dense, row_sel, col_sel):
        rng = self.rng
        kind = rng.randrange(8)
        if kind == 0:
            return rng.choice([0, 1, -1, 2, 7, 0.0])
        if kind == 1:
            return WITHDRAW
        if kind == 2:
            return [rng.randrange(10) for _ in range(rng.randrange(0, 5))]
        if kind == 3:
            return [[rng.randrange(10)] for _ in range(rng.randrange(0, 5))]
        if kind == 4:
            return "overlap"  # selections are pinned inside the try block
        if kind == 5:
            return sparse
        if kind == 6:
            return dense
        return [[rng.randrange(10) for _ in range(rng.randrange(1, 4))]
                for _ in range(rng.randrange(1, 4))]

    def test_random_overlapping_assignments_match_dense_model(self):
        sparse, dense = self._make_pair()
        for trial in range(TRIALS):
            row_sel = random_selector(self.rng, NROWS)
            col_sel = random_selector(self.rng, NCOLS)
            rhs = self._rhs(sparse, dense, row_sel, col_sel)

            s_before = sparse.to_dense()
            d_before = dense.to_dense()
            self.assertEqual(s_before, d_before)

            try:
                if rhs == "overlap":
                    # overlapping source: pin each side before mutation
                    s_rhs = sparse.select(row_sel, col_sel)
                    d_rhs = dense.select(row_sel, col_sel)
                elif rhs is sparse:
                    s_rhs, d_rhs = rhs, sparse.select(
                        row_sel, col_sel, structural=True)
                elif rhs is dense:
                    s_rhs, d_rhs = dense.select(row_sel, col_sel), rhs
                else:
                    s_rhs = d_rhs = rhs
                s_stats = sparse.assign(row_sel, col_sel, s_rhs)
            except (IndexBoundaryError, ShapeMismatchError, TypeError,
                    ValueError) as exc:
                with self.assertRaises(type(exc),
                                      msg=f"trial {trial}: sparse rejected "
                                          f"but dense accepted {exc!r}"):
                    dense.assign(row_sel, col_sel, d_rhs)
                self.assertEqual(sparse.to_dense(), s_before)
                self.assertEqual(dense.to_dense(), d_before)
                continue

            d_stats = dense.assign(row_sel, col_sel, d_rhs)
            self.assertEqual(
                sparse.to_dense(), dense.to_dense(),
                msg=f"trial {trial} numeric mismatch",
            )
            self.assertEqual(
                sparse_presence(sparse), dense.presence_grid(),
                msg=f"trial {trial} presence mismatch",
            )
            self.assertEqual(s_stats, d_stats, msg=f"trial {trial} stats")
            sparse.canonical_check()

    def test_quota_decisions_match_dense_model(self):
        rng = random.Random(SEED + 2)
        entries = [(r, c, rng.randrange(5))
                   for r in range(4) for c in range(4)
                   if rng.random() < 0.4]
        for limit in range(0, 5):
            sparse = SparseMatrix(4, 4, entries)
            dense = DenseReference(4, 4, entries)
            for step in range(80):
                row_sel = [rng.randrange(4) for _ in range(rng.randrange(1, 5))]
                col_sel = [rng.randrange(4) for _ in range(rng.randrange(1, 5))]
                value = rng.choice([0, 1, 9, WITHDRAW])
                qs, qd = MemoryQuota(limit), MemoryQuota(limit)
                before = sparse.to_dense()
                try:
                    s_stats = sparse.assign(row_sel, col_sel, value, quota=qs)
                except QuotaExceededError:
                    with self.assertRaises(QuotaExceededError):
                        dense.assign(row_sel, col_sel, value, quota=qd)
                    self.assertEqual(sparse.to_dense(), before)
                    self.assertEqual(dense.to_dense(), before)
                    self.assertEqual((qs.used, qd.used), (0, 0))
                    continue
                d_stats = dense.assign(row_sel, col_sel, value, quota=qd)
                self.assertEqual(s_stats, d_stats)
                self.assertEqual(sparse.to_dense(), dense.to_dense())
                self.assertEqual(qs.used, qd.used)

    def test_snapshot_history_diverges_like_dense_clone(self):
        rng = random.Random(SEED + 3)
        seed_entries = [(r, 0, 1) for r in range(4)]
        sparse = SparseMatrix(4, 4, seed_entries)
        dense = DenseReference(4, 4, seed_entries)
        history_view = sparse.snapshot()
        history_clone = dense.clone()
        for _ in range(120):
            r = rng.randrange(4)
            c = rng.randrange(4)
            value = rng.choice([0, 2, WITHDRAW])
            sparse.assign(r, c, value)
            dense.assign(r, c, value)
        self.assertEqual(history_view.to_dense(), history_clone.to_dense())
        self.assertEqual(sparse.to_dense(), dense.to_dense())
        sparse.canonical_check()

    def test_explicit_zero_chain_preserved_under_random_overlap(self):
        rng = random.Random(SEED + 4)
        sparse = SparseMatrix(3, 3, [(0, 0, 0), (2, 2, 0)])
        dense = DenseReference(3, 3, [(0, 0, 0), (2, 2, 0)])
        for _ in range(200):
            r, c = rng.randrange(3), rng.randrange(3)
            value = rng.choice([0, 1, WITHDRAW])
            sparse.assign(r, c, value)
            dense.assign(r, c, value)
        self.assertEqual(sparse_presence(sparse, 3, 3),
                         dense.presence_grid())
        self.assertEqual(sparse.to_dense(), dense.to_dense())


if __name__ == "__main__":
    unittest.main(verbosity=2)
