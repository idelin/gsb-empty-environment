"""Runnable walk-through of the sparse matrix toolkit.

Run from the repository root:
    python3.11 examples/demo.py
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from sparsekit import (
    CSR,
    CSC,
    ELIMINATE,
    KEEP,
    STATS,
    dump_batch,
    dumps,
    load_batch,
    loads,
    reset_stats,
)
from sparsekit import SparseMatrix
from sparsekit.serialize import HEADER


def section(title):
    print()
    print("=" * 68)
    print(title)
    print("=" * 68)


def main():
    section("1. duplicate coordinates: exact accumulation, two zero modes")
    rows = [0, 0, 1, 2]
    cols = [0, 0, 1, 2]
    values = [3, -3, 7, 0]
    eliminated = SparseMatrix.from_coo(
        (3, 3), rows, cols, values, mode=ELIMINATE
    )
    kept = SparseMatrix.from_coo((3, 3), rows, cols, values, mode=KEEP)
    print("COO input at (0,0): 3 and -3; explicit 0 at (2,2)")
    print(f"eliminate mode -> nnz={eliminated.nnz}, entries={eliminated.to_coo()}")
    print(
        f"keep mode      -> nnz={kept.nnz}, "
        f"explicit_at(0,0)={kept.explicit_at(0, 0)}, "
        f"explicit_at(2,2)={kept.explicit_at(2, 2)}"
    )

    section("2. canonical CSR / CSC conversion")
    csr = SparseMatrix.from_coo(
        (3, 4),
        [2, 0, 1, 0],
        [1, 3, 0, 3],
        [9, 1, 4, -1],
        layout=CSR,
    )
    csc = csr.to_layout(CSC)
    print(csr)
    print("CSR arrays (indptr, indices, data):", csr.arrays())
    print(csc)
    print("CSC arrays (indptr, indices, data):", csc.arrays())
    print("dense images equal:", csr.to_dense() == csc.to_dense())

    section("3. zero-copy transpose and slice views, bound to versions")
    matrix = SparseMatrix.from_coo(
        (5, 5),
        [0, 1, 2, 3, 4, 1],
        [4, 3, 2, 1, 0, 1],
        [10, 20, 30, 40, 50, 99],
        page_size=4,
    )
    transposed = matrix.T
    window = matrix[1:4, 1:4]
    print(matrix)
    print(transposed)
    print(window)
    print("transpose shares raw buffers:", transposed._data is matrix._data)
    print(f"transpose[3,1] == matrix[1,3]: "
          f"{transposed.get(3, 1)} == {matrix.get(1, 3)}")
    print("window dense image:")
    print(window.to_dense())

    section("4. copy-on-write point update: only the touched page changes")
    reset_stats()
    before = STATS.snapshot()
    updated = matrix.set(1, 1, 777)
    delta = STATS.delta(before)
    old_pages = [id(p) for p in matrix._data.pages]
    new_pages = [id(p) for p in updated._data.pages]
    shared = sum(1 for p in new_pages if p in set(old_pages))
    print(f"data pages: {len(old_pages)}; pages shared after update: {shared}")
    print(f"pages copied: {delta.pages_copied}, elements touched: "
          f"{delta.elements_touched}, nnz: {matrix.nnz}")
    print(f"old view still sees matrix[1,1]={window.get(0, 0)}; "
          f"new version matrix[1,1]={updated.get(1, 1)}")

    section("5. transactional row/column inserts, deletes and assignments")
    with matrix.transaction() as tx:
        tx.insert_rows(2, 1)
        tx.insert_cols(0, 2)
        tx.set(0, 0, 5)
        tx.delete_cols(4, 1)
    result = tx.result
    print(result)
    print(result.to_dense())
    try:
        with matrix.transaction() as tx:
            tx.set(0, 0, 1)
            tx.delete_rows(0, 99)
    except IndexError as error:
        print(f"failed batch rejected atomically: {error}")
    print("base matrix version unchanged:", matrix.version)

    section("6. serialization with size, layout and block checksums")
    blob = dumps(csr)
    print(f"serialized {len(blob)} bytes; restored equals original: "
          f"{loads(blob).to_dense() == csr.to_dense()}")
    corrupted = bytearray(blob)
    corrupted[-5] ^= 0xFF
    try:
        loads(bytes(corrupted))
    except ValueError as error:
        print(f"bad checksum rejected: {error}")
    batch = dump_batch([csr, csc])
    broken_batch = bytearray(batch)
    frame0_len = len(dumps(csr))
    second_payload = 12 + 8 + frame0_len + 8 + HEADER.size + 8
    broken_batch[second_payload] ^= 0x01
    try:
        load_batch(bytes(broken_batch))
    except ValueError as error:
        print(f"mid-batch failure rejected as a whole: {error}")
    print("healthy batch restores", len(load_batch(batch)), "matrices")

    section("7. storage statistics since the last reset")
    print(STATS.snapshot())


if __name__ == "__main__":
    main()
