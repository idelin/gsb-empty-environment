import os
import random
import struct
import sys
import unittest
import zlib

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from sparsekit import (
    CSR,
    CSC,
    ELIMINATE,
    KEEP,
    DenseMatrix,
    PagedBuffer,
    STATS,
    SparseMatrix,
    dump_batch,
    dumps,
    load_batch,
    loads,
    reset_stats,
)
from sparsekit.serialize import (
    BLOCK_CRC,
    BLOCK_COUNT,
    CODE_LAYOUT,
    CODE_MODE,
    FORMAT_VERSION,
    HEADER,
    LAYOUT_CODE,
    MAGIC,
    MODE_CODE,
    pack_block,
)


class PagedBufferPropertyTest(unittest.TestCase):
    def test_splice_window_update_against_list(self):
        rng = random.Random(101)
        for page_size in (1, 2, 3, 5, 8, 16):
            for _ in range(60):
                n = rng.randrange(0, 41)
                values = [rng.randrange(-9, 10) for _ in range(n)]
                buf = PagedBuffer.from_values(values, page_size=page_size)
                self.assertEqual(buf.to_list(), values)
                start = rng.randrange(0, n + 1)
                delete = rng.randrange(0, n - start + 1)
                insert = [rng.randrange(-9, 10) for _ in range(rng.randrange(0, 9))]
                new_buf = buf.splice(start, delete, insert)
                expected = values[:start] + insert + values[start + delete:]
                self.assertEqual(new_buf.to_list(), expected)
                self.assertEqual(buf.to_list(), values)
                if expected:
                    w0 = rng.randrange(0, len(expected))
                    wl = rng.randrange(0, len(expected) - w0 + 1)
                    self.assertEqual(
                        new_buf.window(w0, wl).to_list(),
                        expected[w0:w0 + wl],
                    )
                    idx = rng.randrange(len(expected))
                    updated = new_buf.updated(idx, 777)
                    want = list(expected)
                    want[idx] = 777
                    self.assertEqual(updated.to_list(), want)
                    self.assertEqual(new_buf.to_list(), expected)


class CanonicalConversionTest(unittest.TestCase):
    def test_cancel_pair_disappears_in_eliminate_mode(self):
        matrix = SparseMatrix.from_coo(
            (2, 2), [0, 0, 1], [0, 0, 1], [3, -3, 7], mode=ELIMINATE
        )
        self.assertEqual(matrix.nnz, 1)
        self.assertEqual(matrix.get(0, 0), 0)
        found, _ = matrix.explicit_at(0, 0)
        self.assertFalse(found)
        self.assertEqual(matrix.to_coo(), ([1], [1], [7]))

    def test_explicit_zero_distinguished_in_keep_mode(self):
        matrix = SparseMatrix.from_coo(
            (2, 2), [0, 0, 1], [0, 0, 1], [3, -3, 7], mode=KEEP
        )
        self.assertEqual(matrix.nnz, 2)
        found, value = matrix.explicit_at(0, 0)
        self.assertTrue(found)
        self.assertEqual(value, 0)
        self.assertEqual(matrix.get(0, 0), 0)
        rows, cols, data = matrix.to_coo()
        self.assertEqual(list(zip(rows, cols, data)), [(0, 0, 0), (1, 1, 7)])

    def test_csr_csc_coo_roundtrip(self):
        rng = random.Random(7)
        entries = [
            (rng.randrange(0, 5), rng.randrange(0, 6), rng.randrange(-4, 5))
            for _ in range(80)
        ]
        rows = [e[0] for e in entries]
        cols = [e[1] for e in entries]
        data = [e[2] for e in entries]
        for mode in (ELIMINATE, KEEP):
            csr = SparseMatrix.from_coo((5, 6), rows, cols, data, mode=mode)
            csc = csr.to_layout(CSC)
            self.assertEqual(csc.layout, CSC)
            self.assertEqual(csc.to_dense(), csr.to_dense())
            back = csc.to_layout(CSR)
            self.assertEqual(back.to_coo(), csr.to_coo())
            indptr, indices, values = back.arrays()
            for major in range(5):
                segment = indices[indptr[major]:indptr[major + 1]]
                self.assertEqual(segment, sorted(segment))


class ViewIsolationTest(unittest.TestCase):
    def setUp(self):
        self.matrix = SparseMatrix.from_coo(
            (4, 5),
            [0, 1, 2, 3, 1],
            [0, 2, 4, 1, 2],
            [10, 20, 30, 40, 50],
            layout=CSR,
        )

    def test_transpose_shares_buffers_and_is_readonly(self):
        matrix = self.matrix
        transposed = matrix.T
        self.assertTrue(transposed.is_view)
        self.assertEqual(transposed.layout, CSC)
        self.assertEqual(transposed.shape, (5, 4))
        self.assertEqual(transposed._indptr, matrix._indptr)
        self.assertIs(transposed._data, matrix._data)
        self.assertIs(transposed._indices, matrix._indices)
        self.assertIs(transposed._indptr, matrix._indptr)
        self.assertEqual(transposed.bound_version, matrix.version)
        for r, c, value in matrix.iter_entries():
            self.assertEqual(transposed.get(c, r), value)
        with self.assertRaises(TypeError):
            transposed.set(0, 0, 1)

    def test_slice_view_zero_copy_and_version_bound(self):
        matrix = self.matrix
        before = STATS.snapshot()
        view = matrix[1:4, 1:5]
        delta = STATS.delta(before)
        self.assertEqual(delta.pages_created, 0)
        self.assertEqual(delta.views_created, 1)
        self.assertEqual(view.bound_version, 0)
        self.assertEqual(view.shape, (3, 4))
        self.assertEqual(view.get(0, 1), matrix.get(1, 2))
        self.assertEqual(view.to_dense(), matrix.to_dense().slice(1, 4, 1, 5))
        with self.assertRaises(TypeError):
            view.set(0, 0, 1)

    def test_copy_on_write_keeps_old_views_clean(self):
        matrix = self.matrix
        transposed = matrix.T
        view = matrix[0:2, 0:3]
        old_value = matrix.get(1, 2)
        newer = matrix.set(1, 2, old_value + 999)
        self.assertEqual(newer.version, matrix.version + 1)
        self.assertEqual(matrix.get(1, 2), old_value)
        self.assertEqual(transposed.get(2, 1), old_value)
        self.assertEqual(view.get(1, 2), old_value)
        self.assertEqual(newer.get(1, 2), old_value + 999)
        shared_pages = {id(p) for p in matrix._data.pages} & {
            id(p) for p in newer._data.pages
        }
        self.assertEqual(
            shared_pages,
            {id(p) for p in matrix._data.pages} - {
                id(matrix._data.pages[0])
            },
        )

    def test_only_one_page_copied_per_point_update(self):
        reset_stats()
        page_size = 8
        rng = random.Random(11)
        coords = rng.sample([(r, c) for r in range(40) for c in range(40)], 300)
        rows = [r for r, _ in coords]
        cols = [c for _, c in coords]
        values = [rng.randrange(1, 100) for _ in coords]
        matrix = SparseMatrix.from_coo(
            (40, 40), rows, cols, values, page_size=page_size
        )
        before = STATS.snapshot()
        newer = matrix.set(rows[0], cols[0], 4242)
        delta = STATS.delta(before)
        self.assertEqual(delta.pages_copied, 1)
        self.assertEqual(delta.elements_touched, page_size)
        self.assertEqual(newer.get(rows[0], cols[0]), 4242)


class TransactionTest(unittest.TestCase):
    def test_batched_row_col_changes_match_dense(self):
        matrix = SparseMatrix.from_coo(
            (4, 5),
            [0, 1, 2, 3, 1],
            [0, 2, 4, 1, 2],
            [10, 20, 30, 40, 50],
            mode=KEEP,
        )
        dense = matrix.to_dense()
        dense.insert_rows(2, 2)
        dense.insert_cols(1, 1)
        dense.set(3, 2, 77)
        dense.delete_rows(0, 1)
        dense.delete_cols(2, 2)
        with matrix.transaction() as tx:
            tx.insert_rows(2, 2)
            tx.insert_cols(1, 1)
            tx.set(3, 2, 77)
            tx.delete_rows(0, 1)
            tx.delete_cols(2, 2)
        updated = tx.result
        self.assertEqual(updated.to_dense(), dense)
        self.assertGreaterEqual(updated.version, matrix.version + 1)

    def test_failure_mid_batch_leaves_no_partial_update(self):
        matrix = SparseMatrix.from_coo(
            (4, 5),
            [0, 1, 2, 3, 1],
            [0, 2, 4, 1, 2],
            [10, 20, 30, 40, 50],
        )
        before = matrix.to_dense().to_lists()
        before_version = matrix.version
        tx = matrix.transaction()
        tx.insert_rows(1, 2)
        tx.set(0, 0, 5)
        tx.set(99, 99, 1)
        with self.assertRaises(IndexError):
            tx.commit()
        self.assertEqual(matrix.to_dense().to_lists(), before)
        self.assertEqual(matrix.version, before_version)
        with self.assertRaises(IndexError):
            with matrix.transaction() as broken:
                broken.set(0, 0, 1)
                broken.delete_rows(0, 20)
        self.assertEqual(matrix.to_dense().to_lists(), before)
        self.assertEqual(matrix.version, before_version)

    def test_view_cannot_open_transaction(self):
        view = self_matrix_view()
        with self.assertRaises(TypeError):
            view.transaction()


def self_matrix_view():
    matrix = SparseMatrix.from_coo(
        (3, 3), [0, 1], [0, 1], [1, 2]
    )
    return matrix[0:2, :]


class SerializationTest(unittest.TestCase):
    def setUp(self):
        self.matrix = SparseMatrix.from_coo(
            (3, 4),
            [0, 2, 1, 0],
            [3, 0, 1, 1],
            [1.5, 2.5, 3.5, -4.0],
            layout=CSR,
            mode=KEEP,
        )

    def test_roundtrip_preserves_size_layout_mode(self):
        blob = dumps(self.matrix)
        restored = loads(blob)
        self.assertEqual(restored.shape, self.matrix.shape)
        self.assertEqual(restored.layout, self.matrix.layout)
        self.assertEqual(restored.mode, self.matrix.mode)
        self.assertEqual(restored.to_coo(), self.matrix.to_coo())
        csc = self.matrix.to_layout(CSC)
        self.assertEqual(loads(dumps(csc)).layout, CSC)

    def _encode(self, shape, layout, mode, indptr, indices, data, version=0):
        nrows, ncols = shape
        header = HEADER.pack(
            MAGIC,
            FORMAT_VERSION,
            version,
            nrows,
            ncols,
            len(data),
            LAYOUT_CODE[layout],
            MODE_CODE[mode],
        )
        return (
            header
            + pack_block(indptr, "q")
            + pack_block(indices, "q")
            + pack_block([float(v) for v in data], "d")
        )

    def test_misaligned_column_pointer_rejected(self):
        csc = self.matrix.to_layout(CSC)
        indptr, indices, data = csc.arrays()
        broken = list(indptr)
        broken[1], broken[2] = broken[2], broken[1]
        blob = self._encode(csc.shape, CSC, csc.mode, broken, indices, data)
        with self.assertRaises(ValueError):
            loads(blob)

    def test_bad_checksum_rejected(self):
        blob = bytearray(dumps(self.matrix))
        data_start = HEADER.size
        data_start += BLOCK_COUNT.size + len(self.matrix.arrays()[0]) * 8
        data_start += BLOCK_CRC.size
        data_start += BLOCK_COUNT.size + len(self.matrix.arrays()[1]) * 8
        data_start += BLOCK_CRC.size
        data_start += BLOCK_COUNT.size
        blob[data_start] ^= 0xFF
        with self.assertRaises(ValueError):
            loads(bytes(blob))

    def test_batch_mid_failure_rejects_everything(self):
        matrices = [
            SparseMatrix.from_coo((2, 2), [0], [0], [1.0]),
            SparseMatrix.from_coo((2, 2), [1], [1], [2.0]),
            SparseMatrix.from_coo((2, 2), [0, 1], [1, 0], [3.0, 4.0]),
        ]
        blob = bytearray(dump_batch(matrices))
        second_offset = 12 + len(dumps(matrices[0]))
        blob[second_offset + HEADER.size + BLOCK_COUNT.size] ^= 0x01
        with self.assertRaises(ValueError):
            load_batch(bytes(blob))
        with self.assertRaises(ValueError):
            load_batch(bytes(blob[: len(blob) - 3]))
        good = load_batch(dump_batch(matrices))
        self.assertEqual(len(good), 3)


class RandomizedDenseComparisonTest(unittest.TestCase):
    def test_random_update_streams(self):
        rng = random.Random(20240925)
        for trial in range(30):
            nrows = rng.randrange(1, 7)
            ncols = rng.randrange(1, 7)
            mode = rng.choice((ELIMINATE, KEEP))
            rows = [rng.randrange(nrows) for _ in range(25)]
            cols = [rng.randrange(ncols) for _ in range(25)]
            values = [rng.randrange(-4, 5) for _ in range(25)]
            matrix = SparseMatrix.from_coo(
                (nrows, ncols), rows, cols, values, mode=mode
            )
            dense = DenseMatrix.zeros(nrows, ncols)
            for r, c, v in zip(rows, cols, values):
                dense.set(r, c, dense.get(r, c) + v)
            self.assertEqual(matrix.to_dense(), dense)
            for step in range(40):
                op = rng.randrange(7)
                if op == 0:
                    r = rng.randrange(nrows)
                    c = rng.randrange(ncols)
                    v = rng.randrange(-5, 6)
                    matrix = matrix.set(r, c, v)
                    dense.set(r, c, v)
                elif op == 1 and nrows < 10:
                    k = rng.randrange(0, nrows + 1)
                    matrix = matrix.insert_rows(k, 1)
                    dense.insert_rows(k, 1)
                    nrows += 1
                elif op == 2 and nrows > 1:
                    k = rng.randrange(nrows)
                    matrix = matrix.delete_rows(k, 1)
                    dense.delete_rows(k, 1)
                    nrows -= 1
                elif op == 3 and ncols < 10:
                    k = rng.randrange(0, ncols + 1)
                    matrix = matrix.insert_cols(k, 1)
                    dense.insert_cols(k, 1)
                    ncols += 1
                elif op == 4 and ncols > 1:
                    k = rng.randrange(ncols)
                    matrix = matrix.delete_cols(k, 1)
                    dense.delete_cols(k, 1)
                    ncols -= 1
                elif op == 5:
                    transposed = matrix.T
                    reference = dense.transpose()
                    self.assertEqual(transposed.shape, reference.shape)
                    self.assertEqual(transposed.to_dense(), reference)
                else:
                    r0 = rng.randrange(0, nrows)
                    r1 = rng.randrange(r0 + 1, nrows + 1)
                    c0 = rng.randrange(0, ncols)
                    c1 = rng.randrange(c0 + 1, ncols + 1)
                    view = matrix[r0:r1, c0:c1]
                    self.assertEqual(view.bound_version, matrix.version)
                    self.assertEqual(
                        view.to_dense(), dense.slice(r0, r1, c0, c1)
                    )
                if step % 5 == 0:
                    self.assertEqual(matrix.shape, dense.shape)
                    self.assertEqual(matrix.to_dense(), dense)
            self.assertEqual(matrix.shape, dense.shape)
            self.assertEqual(matrix.to_dense(), dense)


if __name__ == "__main__":
    unittest.main(verbosity=2)
