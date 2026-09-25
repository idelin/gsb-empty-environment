import random
import unittest

from bitmap import Bitmap, BitmapLengthError, CHUNK_BITS, SPARSE_THRESHOLD


class BitmapOpsTest(unittest.TestCase):
    def test_and_or_not_basic(self):
        a = Bitmap.from_indices(10, [1, 3, 5, 7])
        b = Bitmap.from_indices(10, [3, 4, 5])
        self.assertEqual((a & b).to_indices(), [3, 5])
        self.assertEqual((a | b).to_indices(), [1, 3, 4, 5, 7])
        self.assertEqual((~a).to_indices(), [0, 2, 4, 6, 8, 9])

    def test_length_mismatch_raises(self):
        a = Bitmap.zeros(8)
        b = Bitmap.zeros(9)
        with self.assertRaises(BitmapLengthError):
            a & b
        with self.assertRaises(BitmapLengthError):
            a | b
        with self.assertRaises(BitmapLengthError):
            Bitmap.ones(CHUNK_BITS) & Bitmap.ones(CHUNK_BITS + 1)

    def test_combine_with_non_bitmap_raises(self):
        with self.assertRaises(TypeError):
            Bitmap.zeros(3) & [1, 2]

    def test_not_flips_only_valid_range(self):
        n = CHUNK_BITS + 70  # final segment only partially used
        bm = ~Bitmap.zeros(n)
        self.assertEqual(bm.count(), n)
        self.assertEqual(max(bm), n - 1)
        self.assertEqual((~bm).count(), 0)
        self.assertFalse(any(i >= n for i in bm))

    def test_tail_padding_bits_never_leak(self):
        # White-box: no stored word may hold a bit at index >= len(bitmap).
        for n in (1, 5, 63, 64, 65, 70, CHUNK_BITS - 1, CHUNK_BITS + 1):
            for bm in (Bitmap.ones(n),
                       ~Bitmap.zeros(n),
                       ~(Bitmap.ones(n) & Bitmap.from_indices(n, [0])),
                       Bitmap.from_indices(n, range(0, n, 3)) | Bitmap.ones(n)):
                self.assertEqual(bm.count(), len(list(bm)))
                for si, seg in enumerate(bm._segments):
                    if seg is None:
                        continue
                    for wi, w in enumerate(seg.to_words()):
                        if w:
                            highest = si * CHUNK_BITS + wi * 64 \
                                + w.bit_length() - 1
                            self.assertLess(highest, n)

    def test_rank_select_roundtrip(self):
        rng = random.Random(42)
        n = 3 * CHUNK_BITS + 123
        idx = sorted(rng.sample(range(n), 5000))
        bm = Bitmap.from_indices(n, idx)
        for i in idx:
            self.assertEqual(bm.select(bm.rank(i)), i)
        self.assertEqual(bm.rank(idx[0]), 1)          # rank is inclusive
        self.assertEqual(bm.rank(n - 1), bm.count())
        self.assertEqual(bm.rank(n + 100), bm.count())  # clamped above
        self.assertEqual(bm.rank(-1), 0)                # clamped below

    def test_select_bounds(self):
        bm = Bitmap.from_indices(100, [10, 20, 30])
        self.assertEqual(bm.select(1), 10)
        self.assertEqual(bm.select(3), 30)
        with self.assertRaises(IndexError):
            bm.select(0)
        with self.assertRaises(IndexError):
            bm.select(4)

    def test_empty_bitmap_boundaries(self):
        e = Bitmap.zeros(0)
        self.assertEqual(len(e), 0)
        self.assertEqual(e.count(), 0)
        self.assertFalse(e)
        self.assertEqual(list(e), [])
        self.assertEqual(e.rank(0), 0)
        self.assertEqual(e.rank(100), 0)
        self.assertEqual(e.rank(-5), 0)
        with self.assertRaises(IndexError):
            e.select(1)
        self.assertEqual((~e).count(), 0)
        self.assertEqual(len(~e), 0)
        self.assertEqual((e & Bitmap.zeros(0)).count(), 0)
        self.assertEqual((e | Bitmap.zeros(0)).count(), 0)
        with self.assertRaises(BitmapLengthError):
            e & Bitmap.zeros(1)

    def test_sparse_and_dense_segments(self):
        n = 3 * CHUNK_BITS
        dense_region = list(range(0, CHUNK_BITS, 2))      # 2048 bits -> dense
        sparse_region = [CHUNK_BITS + 7, CHUNK_BITS + 3000]  # 2 bits -> sparse
        bm = Bitmap.from_indices(n, dense_region + sparse_region)
        self.assertEqual(bm.segment_kinds(), ["dense", "sparse", "empty"])
        self.assertEqual(bm.count(), len(dense_region) + 2)
        # threshold boundary: exactly SPARSE_THRESHOLD bits stays sparse
        edge = Bitmap.from_indices(CHUNK_BITS, range(SPARSE_THRESHOLD))
        self.assertEqual(edge.segment_kinds(), ["sparse"])
        edge = Bitmap.from_indices(CHUNK_BITS, range(SPARSE_THRESHOLD + 1))
        self.assertEqual(edge.segment_kinds(), ["dense"])

    def test_ops_preserve_compression_and_values(self):
        n = 2 * CHUNK_BITS
        dense = Bitmap.from_indices(n, range(0, CHUNK_BITS, 3))
        sparse = Bitmap.from_indices(n, [CHUNK_BITS + 5, CHUNK_BITS + 900])
        both = dense | sparse
        self.assertEqual(both.segment_kinds(), ["dense", "sparse"])
        self.assertEqual((both & sparse).to_indices(), sparse.to_indices())
        self.assertEqual((both & dense).to_indices(), dense.to_indices())

    def test_random_against_set_model(self):
        rng = random.Random(7)
        n = 2 * CHUNK_BITS + 500

        def rand_bm():
            p = rng.choice([0.01, 0.5, 0.99])
            return Bitmap.from_indices(
                n, [i for i in range(n) if rng.random() < p])

        universe = set(range(n))
        for _ in range(25):
            a, b = rand_bm(), rand_bm()
            sa, sb = set(a), set(b)
            self.assertEqual(set(a & b), sa & sb)
            self.assertEqual(set(a | b), sa | sb)
            self.assertEqual(set(~a), universe - sa)
            self.assertEqual((a & b).count(), len(sa & sb))
            sample = rng.sample(sorted(sa), min(10, len(sa)))
            for i in sample:
                self.assertEqual(a.select(a.rank(i)), i)
                self.assertIn(i, a)
            for k in range(1, min(5, len(sa)) + 1):
                self.assertEqual(a.select(k), sorted(sa)[k - 1])

    def test_from_bools_and_equality(self):
        bm = Bitmap.from_bools([True, False, True, True])
        self.assertEqual(bm.to_indices(), [0, 2, 3])
        self.assertEqual(bm, Bitmap.from_indices(4, [0, 2, 3]))
        self.assertNotEqual(bm, Bitmap.from_indices(5, [0, 2, 3]))
        self.assertEqual(bm & Bitmap.ones(4), bm)
        self.assertEqual(bm | Bitmap.zeros(4), bm)

    def test_constructor_validation(self):
        with self.assertRaises(ValueError):
            Bitmap.zeros(-1)
        with self.assertRaises(IndexError):
            Bitmap.from_indices(4, [4])
        with self.assertRaises(IndexError):
            Bitmap.from_indices(0, [0])


if __name__ == "__main__":
    unittest.main()
