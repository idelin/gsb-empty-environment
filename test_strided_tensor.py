"""Unit tests for strided_tensor, including brute-force address-set
verification of every overlap predicate on small random views."""

import random
import unittest

from strided_tensor import (
    BorrowError,
    BoundsError,
    NonContiguousError,
    Overlap,
    OverlapError,
    Storage,
    Tensor,
    internal_overlap,
    may_overlap,
    offset_set,
    views_overlap,
)


def random_view(rng, storage):
    """Build a random in-bounds view (or None if it does not fit)."""
    ndim = rng.randint(1, 3)
    shape = tuple(rng.randint(0, 4) for _ in range(ndim))
    strides = tuple(rng.randint(-5, 5) for _ in range(ndim))
    if 0 in shape:
        return Tensor(storage, shape, strides, 0)
    lo = hi = 0
    for n, s in zip(shape, strides):
        ext = (n - 1) * s
        lo, hi = (lo + ext, hi) if s < 0 else (lo, hi + ext)
    span = hi - lo
    if span > len(storage.data) - 1:
        return None
    offset = -lo + rng.randint(0, len(storage.data) - 1 - span)
    return Tensor(storage, shape, strides, offset)


class TestViews(unittest.TestCase):
    def test_basic_get_set(self):
        t = Tensor.from_flat(list(range(6)), (2, 3))
        self.assertEqual(t.get(1, 2), 5)
        t.set((0, 1), 99)
        self.assertEqual(t.get(0, 1), 99)
        self.assertEqual(t.to_list(), [[0, 99, 2], [3, 4, 5]])

    def test_transpose_shares_storage(self):
        t = Tensor.from_flat(list(range(6)), (2, 3))
        tr = t.T
        self.assertEqual(tr.shape, (3, 2))
        self.assertEqual(tr.strides, (1, 3))
        # independent numerical reference computed by hand from the data
        data = list(range(6))
        expected = [[data[r * 3 + c] for r in range(2)] for c in range(3)]
        self.assertEqual(tr.to_list(), expected)
        tr.set((2, 1), 42)  # write through the transposed view
        self.assertEqual(t.get(1, 2), 42)

    def test_negative_stride_reverse_reads(self):
        t = Tensor.arange(6)
        rev = t[::-1]
        self.assertEqual(rev.strides, (-1,))
        self.assertEqual(rev.to_list(), [5, 4, 3, 2, 1, 0])
        self.assertEqual(rev.get(0), 5)
        self.assertEqual(rev.get(5), 0)
        m = Tensor.from_flat(list(range(6)), (2, 3))
        flip = m[::-1, ::-1]
        self.assertEqual(flip.to_list(), [[5, 4, 3], [2, 1, 0]])
        neg = t[5:1:-2]
        self.assertEqual(neg.to_list(), [5, 3])

    def test_noncontiguous_slice(self):
        t = Tensor.arange(10)
        evens = t[::2]
        self.assertEqual(evens.to_list(), [0, 2, 4, 6, 8])
        self.assertFalse(evens.is_contiguous)
        mid = t[1:9:3]
        self.assertEqual(mid.to_list(), [1, 4, 7])
        m = Tensor.from_flat(list(range(12)), (3, 4))
        col = m[:, 1]
        self.assertEqual(col.to_list(), [1, 5, 9])
        sub = m[::2, 1::2]
        self.assertEqual(sub.to_list(), [[1, 3], [9, 11]])

    def test_zero_stride_broadcast(self):
        t = Tensor.arange(3)
        b = t[1:2].broadcast_to((4,))
        self.assertEqual(b.strides, (0,))
        self.assertEqual(b.to_list(), [1, 1, 1, 1])
        self.assertIs(b.internal_overlap(), Overlap.YES)
        row = Tensor.from_flat([7, 8, 9], (1, 3)).broadcast_to((2, 3))
        self.assertEqual(row.to_list(), [[7, 8, 9], [7, 8, 9]])
        self.assertIs(row.internal_overlap(), Overlap.YES)

    def test_empty_dims(self):
        t = Tensor.from_flat([], (0, 3))
        self.assertEqual(t.numel, 0)
        self.assertEqual(t.to_list(), [])
        self.assertIs(t.internal_overlap(), Overlap.NO)
        other = Tensor.arange(4)
        self.assertIs(views_overlap(t, other), Overlap.NO)
        empty_slice = Tensor.arange(5)[2:2]
        self.assertEqual(empty_slice.numel, 0)
        self.assertIs(views_overlap(empty_slice, Tensor.arange(5)), Overlap.NO)

    def test_permute(self):
        t = Tensor.from_flat(list(range(24)), (2, 3, 4))
        p = t.permute(2, 0, 1)
        self.assertEqual(p.shape, (4, 2, 3))
        self.assertEqual(p.get(3, 1, 2), t.get(1, 2, 3))


class TestOverlapAnalysis(unittest.TestCase):
    def test_internal_alias_detected_exactly(self):
        # strides (2, 2) fail the nested-stride sufficient condition and
        # collide: (0,1) and (1,0) both map to offset 2.
        t = Tensor(Storage(range(8)), (2, 2), (2, 2), 0)
        self.assertIs(internal_overlap(t), Overlap.YES)

    def test_internal_non_alias_tricky(self):
        # fails the sufficient condition but is actually injective:
        # offsets {0,2,4,3,5,7}
        t = Tensor(Storage(range(8)), (2, 3), (3, 2), 0)
        self.assertIs(internal_overlap(t), Overlap.NO)

    def test_even_odd_slices_disjoint_despite_intervals(self):
        t = Tensor.arange(8)
        evens, odds = t[0:8:2], t[1:8:2]
        # bounding intervals [0,6] and [1,7] DO intersect ...
        self.assertFalse(evens.absolute_bounds()[1] < odds.absolute_bounds()[0])
        # ... but the address sets are disjoint: not decided by interval alone
        self.assertIs(views_overlap(evens, odds), Overlap.NO)
        self.assertFalse(may_overlap(evens, odds))

    def test_overlap_yes(self):
        t = Tensor.arange(8)
        a, b = t[0:5], t[3:8]
        self.assertIs(views_overlap(a, b), Overlap.YES)
        self.assertIs(views_overlap(a, a), Overlap.YES)

    def test_disjoint_intervals_fast_path(self):
        t = Tensor.arange(8)
        self.assertIs(views_overlap(t[0:3], t[5:8]), Overlap.NO)

    def test_different_storage_never_overlaps(self):
        self.assertIs(views_overlap(Tensor.arange(4), Tensor.arange(4)), Overlap.NO)

    def test_unknown_is_conservatively_unsafe(self):
        # a view that needs the exact solver; with limit 0 it is UNKNOWN
        t = Tensor(Storage(range(8)), (2, 3), (3, 2), 0)
        self.assertIs(internal_overlap(t, exact_limit=0), Overlap.UNKNOWN)
        a = Tensor.arange(100)
        x, y = a[0:50], a[25:75]
        self.assertIs(views_overlap(x, y, exact_limit=10), Overlap.UNKNOWN)
        self.assertTrue(may_overlap(x, y))  # UNKNOWN treated as unsafe
        with self.assertRaises(OverlapError):
            x.add_(y)

    def test_random_views_match_address_sets(self):
        # Every predicate answer is verified against brute-force address-set
        # enumeration -- never against bounding byte ranges.
        rng = random.Random(20261005)
        checked = 0
        for _ in range(1200):
            storage = Storage(range(rng.randint(1, 24)))
            a = random_view(rng, storage)
            b = random_view(rng, storage)
            if a is None or b is None:
                continue
            # internal overlap <-> address set smaller than element count
            expect_internal = len(offset_set(a)) != a.numel
            got = internal_overlap(a)
            self.assertIsNot(got, Overlap.UNKNOWN)  # small views: exact
            self.assertEqual(got is Overlap.YES, expect_internal)
            # pairwise overlap <-> non-empty address-set intersection
            expect_pair = bool(offset_set(a) & offset_set(b))
            got_pair = views_overlap(a, b)
            self.assertIsNot(got_pair, Overlap.UNKNOWN)
            self.assertEqual(got_pair is Overlap.YES, expect_pair)
            checked += 1
        self.assertGreater(checked, 300)


class TestInPlaceSafety(unittest.TestCase):
    def test_copy_snapshot_semantics(self):
        t = Tensor.arange(6)  # [0,1,2,3,4,5]
        dst, src = t[1:5], t[0:4]
        self.assertIs(views_overlap(dst, src), Overlap.YES)
        dst.copy_(src)
        # snapshot: dst sees the ORIGINAL src values, no smearing
        self.assertEqual(t.to_list(), [0, 0, 1, 2, 3, 5])

    def test_copy_non_overlapping(self):
        t = Tensor.arange(6)
        t[0:2].copy_(t[4:6])
        self.assertEqual(t.to_list(), [4, 5, 2, 3, 4, 5])

    def test_copy_into_self_aliasing_destination_rejected(self):
        t = Tensor.arange(3)
        dst = t[0:1].broadcast_to((3,))  # zero-stride destination
        with self.assertRaises(OverlapError):
            dst.copy_(Tensor.arange(3))

    def test_add_overlap_rejected(self):
        t = Tensor.arange(4)
        with self.assertRaises(OverlapError):
            t.add_(t[::-1])
        with self.assertRaises(OverlapError):
            t[0:3].add_(t[1:4])

    def test_add_non_overlapping(self):
        t = Tensor.arange(4)
        t[0:2].add_(t[2:4])
        self.assertEqual(t.to_list(), [2, 4, 2, 3])

    def test_fill(self):
        t = Tensor.arange(4)
        t[::2].fill_(9)
        self.assertEqual(t.to_list(), [9, 1, 9, 3])

    def test_borrow_write_protection(self):
        t = Tensor.arange(4)
        view = t[::2]
        with t.borrowed() as ro:
            self.assertEqual(ro.get(2), 2)  # reads still work
            for op in (
                lambda: t.set(0, 9),
                lambda: t.fill_(0),
                lambda: view.fill_(0),
                lambda: t[0:2].copy_(t[2:4]),
                lambda: t[0:2].add_(Tensor.arange(2)),
            ):
                with self.assertRaises(BorrowError):
                    op()
        t.set(0, 9)  # borrow released: writes work again
        self.assertEqual(t.get(0), 9)


class TestReshapeAndBounds(unittest.TestCase):
    def test_reshape_contiguous(self):
        t = Tensor.arange(6)
        r = t.reshape(2, 3)
        self.assertEqual(r.to_list(), [[0, 1, 2], [3, 4, 5]])
        r.set((1, 0), 30)  # still a view of the same storage
        self.assertEqual(t.get(3), 30)

    def test_reshape_transposed_rejected_then_copied(self):
        t = Tensor.from_flat(list(range(6)), (2, 3))
        tr = t.T
        self.assertFalse(tr.is_contiguous)
        with self.assertRaises(NonContiguousError):
            tr.reshape(6)
        flat = tr.contiguous().reshape(6)
        self.assertEqual(flat.to_list(), [0, 3, 1, 4, 2, 5])

    def test_reshape_size_mismatch(self):
        with self.assertRaises(ValueError):
            Tensor.arange(6).reshape(4, 2)

    def test_bounds_not_decided_by_numel(self):
        # numel 2 fits in 4 cells, but stride 100 reaches far outside
        with self.assertRaises(BoundsError):
            Tensor(Storage(range(4)), (2,), (100,), 0)
        with self.assertRaises(BoundsError):
            Tensor(Storage(range(4)), (2, 2), (1, 3), 1)
        with self.assertRaises(BoundsError):
            Tensor(Storage(range(4)), (2,), (-1,), 0)  # reaches offset -1
        # negative-stride view that DOES fit is accepted
        ok = Tensor(Storage(range(4)), (3,), (-1,), 2)
        self.assertEqual(ok.to_list(), [2, 1, 0])

    def test_index_bounds(self):
        t = Tensor.from_flat(list(range(6)), (2, 3))
        with self.assertRaises(BoundsError):
            t.get(2, 0)
        with self.assertRaises(BoundsError):
            t.get(0, 3)
        with self.assertRaises(BoundsError):
            t[5]
        with self.assertRaises(BoundsError):
            t.get(0, 0, 0)  # wrong arity


class TestIndependentNumerics(unittest.TestCase):
    """Cross-check view reads against independently computed references."""

    def test_random_slice_reads_match_python_slicing(self):
        rng = random.Random(7)
        for _ in range(200):
            n = rng.randint(0, 30)
            data = [rng.randint(-50, 50) for _ in range(n)]
            t = Tensor(Storage(data), (n,))
            sl = slice(
                rng.choice([None, *range(-n, n + 1)]) if n else None,
                rng.choice([None, *range(-n, n + 1)]) if n else None,
                rng.choice([None, -3, -2, -1, 1, 2, 3]),
            )
            self.assertEqual(t[sl].to_list(), data[sl])

    def test_2d_view_reads_match_nested_reference(self):
        rng = random.Random(11)
        rows, cols = 4, 5
        data = [[rng.randint(0, 99) for _ in range(cols)] for _ in range(rows)]
        t = Tensor.from_flat([x for r in data for x in r], (rows, cols))
        self.assertEqual(t.T.to_list(), [[data[r][c] for r in range(rows)]
                                         for c in range(cols)])
        self.assertEqual(t[::-1].to_list(), [list(reversed(data))][0]
                         and [data[r] for r in reversed(range(rows))])
        self.assertEqual(t[:, ::-1].to_list(),
                         [list(reversed(row)) for row in data])
        self.assertEqual(t[1:3, 1:4:2].to_list(),
                         [[data[r][c] for c in range(1, 4, 2)]
                          for r in range(1, 3)])


if __name__ == "__main__":
    unittest.main()
