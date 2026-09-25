import unittest

from rowselect.bitmap import _CHUNK_BITS, _FULL, Bitmap


class TestConstruction(unittest.TestCase):
    def test_zeros_and_ones(self):
        self.assertEqual(Bitmap.zeros(10).to_indices(), [])
        self.assertEqual(Bitmap.ones(10).to_indices(), list(range(10)))

    def test_from_indices_roundtrip(self):
        bm = Bitmap.from_indices(1000, [0, 3, 511, 512, 999])
        self.assertEqual(bm.to_indices(), [0, 3, 511, 512, 999])
        self.assertEqual(len(bm), 1000)
        self.assertIn(512, bm)
        self.assertNotIn(513, bm)
        self.assertNotIn(1000, bm)  # out of range, not an error

    def test_from_bits(self):
        bm = Bitmap.from_bits([True, False, True, True])
        self.assertEqual(bm.to_indices(), [0, 2, 3])

    def test_negative_length_rejected(self):
        with self.assertRaises(ValueError):
            Bitmap(-1)

    def test_index_out_of_range_rejected(self):
        with self.assertRaises(IndexError):
            Bitmap.from_indices(5, [5])


class TestComposition(unittest.TestCase):
    def setUp(self):
        self.a = Bitmap.from_indices(16, [1, 2, 3, 10])
        self.b = Bitmap.from_indices(16, [3, 4, 10, 11])

    def test_and(self):
        self.assertEqual((self.a & self.b).to_indices(), [3, 10])

    def test_or(self):
        self.assertEqual((self.a | self.b).to_indices(), [1, 2, 3, 4, 10, 11])

    def test_xor(self):
        self.assertEqual((self.a ^ self.b).to_indices(), [1, 2, 4, 11])

    def test_not(self):
        self.assertEqual((~self.a).to_indices(),
                         [0, 4, 5, 6, 7, 8, 9, 11, 12, 13, 14, 15])

    def test_double_not_is_identity(self):
        self.assertEqual(~~self.a, self.a)

    def test_de_morgan(self):
        self.assertEqual(~(self.a & self.b), ~self.a | ~self.b)
        self.assertEqual(~(self.a | self.b), ~self.a & ~self.b)

    def test_length_mismatch_raises(self):
        short = Bitmap.zeros(8)
        long = Bitmap.zeros(9)
        for op in (Bitmap.__and__, Bitmap.__or__, Bitmap.__xor__):
            with self.assertRaises(ValueError):
                op(short, long)
            with self.assertRaises(ValueError):
                op(long, short)

    def test_combine_with_non_bitmap_raises(self):
        with self.assertRaises(TypeError):
            self.a & 42


class TestNotTailPadding(unittest.TestCase):
    """NOT must only flip valid rows; padding bits must never leak."""

    def test_not_zeros_small(self):
        bm = ~Bitmap.zeros(3)
        self.assertEqual(bm.to_indices(), [0, 1, 2])
        self.assertEqual(bm.popcount(), 3)
        with self.assertRaises(IndexError):
            bm.select(3)

    def test_not_ones_is_empty(self):
        self.assertEqual((~Bitmap.ones(7)).popcount(), 0)

    def test_not_across_chunk_boundary(self):
        # Length is not a multiple of the chunk size: last chunk has padding.
        length = _CHUNK_BITS + 100
        bm = ~Bitmap.zeros(length)
        self.assertEqual(bm.popcount(), length)
        self.assertEqual(bm.to_indices(), list(range(length)))
        # Iteration must stop at length; no padding bits beyond it.
        self.assertNotIn(length, bm)

    def test_padding_bits_stay_zero_after_ops(self):
        length = _CHUNK_BITS * 2 + 1
        bm = (~Bitmap.zeros(length)) | Bitmap.zeros(length)
        self.assertEqual(bm.popcount(), length)
        last_chunk = bm._chunks[-1]
        self.assertEqual(last_chunk, 1)  # only the single valid bit, no padding


class TestRankSelect(unittest.TestCase):
    def test_rank_basic(self):
        bm = Bitmap.from_indices(10, [2, 5, 7])
        self.assertEqual([bm.rank(i) for i in range(11)],
                         [0, 0, 0, 1, 1, 1, 2, 2, 3, 3, 3])

    def test_select_basic(self):
        bm = Bitmap.from_indices(10, [2, 5, 7])
        self.assertEqual([bm.select(k) for k in range(3)], [2, 5, 7])

    def test_select_rank_identity(self):
        # select(rank(i)) == i exactly when row i is selected.
        indices = [0, 1, 511, 512, 513, 700, 1023, 1024, 5000]
        bm = Bitmap.from_indices(6000, indices)
        for i in indices:
            self.assertEqual(bm.select(bm.rank(i)), i)

    def test_rank_select_inverse_over_dense_and_sparse(self):
        bm = Bitmap.from_indices(
            3000, list(range(0, 600)) + list(range(1500, 3000, 7))
        )
        for k, i in enumerate(bm):
            self.assertEqual(bm.rank(i), k)
            self.assertEqual(bm.select(k), i)

    def test_rank_boundaries(self):
        bm = Bitmap.from_indices(8, [0, 7])
        self.assertEqual(bm.rank(0), 0)
        self.assertEqual(bm.rank(8), 2)
        with self.assertRaises(IndexError):
            bm.rank(9)
        with self.assertRaises(IndexError):
            bm.rank(-1)

    def test_select_out_of_range(self):
        bm = Bitmap.from_indices(8, [0, 7])
        with self.assertRaises(IndexError):
            bm.select(2)
        with self.assertRaises(IndexError):
            bm.select(-1)

    def test_popcount(self):
        bm = Bitmap.from_indices(1000, range(0, 1000, 3))
        self.assertEqual(bm.popcount(), 334)


class TestEmptyBitmap(unittest.TestCase):
    def test_empty_length_zero(self):
        bm = Bitmap.zeros(0)
        self.assertEqual(len(bm), 0)
        self.assertEqual(bm.popcount(), 0)
        self.assertEqual(bm.rank(0), 0)
        self.assertEqual(bm.to_indices(), [])
        self.assertFalse(bm)
        with self.assertRaises(IndexError):
            bm.rank(1)
        with self.assertRaises(IndexError):
            bm.select(0)

    def test_empty_not_is_still_empty(self):
        bm = ~Bitmap.zeros(0)
        self.assertEqual(len(bm), 0)
        self.assertEqual(bm.popcount(), 0)

    def test_empty_combines_with_empty(self):
        self.assertEqual(Bitmap.zeros(0) & Bitmap.zeros(0), Bitmap.zeros(0))
        self.assertEqual(Bitmap.zeros(0) | Bitmap.zeros(0), Bitmap.zeros(0))

    def test_empty_vs_nonempty_mismatch(self):
        with self.assertRaises(ValueError):
            Bitmap.zeros(0) | Bitmap.zeros(1)

    def test_all_zero_selection(self):
        bm = Bitmap.zeros(100)
        self.assertEqual(bm.popcount(), 0)
        self.assertFalse(bm)
        with self.assertRaises(IndexError):
            bm.select(0)


class TestCompression(unittest.TestCase):
    def test_dense_region_uses_full_sentinel(self):
        bm = Bitmap.ones(_CHUNK_BITS * 3)
        self.assertEqual(bm._chunks, [_FULL, _FULL, _FULL])

    def test_sparse_region_stays_zero(self):
        bm = Bitmap.from_indices(_CHUNK_BITS * 10, [3])
        self.assertEqual(bm._chunks[0], 1 << 3)
        self.assertTrue(all(c == 0 for c in bm._chunks[1:]))

    def test_ops_renormalize_segments(self):
        full = Bitmap.ones(_CHUNK_BITS * 2)
        bm = full & ~full
        self.assertEqual(bm._chunks, [0, 0])
        bm2 = Bitmap.zeros(_CHUNK_BITS * 2) | full
        self.assertEqual(bm2._chunks, [_FULL, _FULL])

    def test_mixed_sparse_and_dense(self):
        indices = list(range(_CHUNK_BITS)) + [_CHUNK_BITS * 4 + 5]
        bm = Bitmap.from_indices(_CHUNK_BITS * 5, indices)
        self.assertEqual(bm._chunks[0], _FULL)          # dense region
        self.assertEqual(bm._chunks[1], 0)              # sparse gap
        self.assertEqual(bm._chunks[4], 1 << 5)         # sparse tail
        self.assertEqual(bm.popcount(), _CHUNK_BITS + 1)


class TestLargeBitmap(unittest.TestCase):
    def test_every_third_row(self):
        n = 100_000
        bm = Bitmap.from_indices(n, range(0, n, 3))
        self.assertEqual(bm.popcount(), (n + 2) // 3)
        for i in range(0, n, 3 * 977):  # sample across sparse/dense chunks
            self.assertEqual(bm.rank(i), i // 3)
            self.assertEqual(bm.select(i // 3), i)
        self.assertEqual(bm.select(bm.rank(99_999)), 99_999)


if __name__ == "__main__":
    unittest.main()
