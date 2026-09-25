# sparsekit — COO/CSR/CSC, zero-copy views, copy-on-write pages

Pure Python 3.11 standard-library implementation. No NumPy or SciPy.

## Features

- **Canonical conversion**: build from COO with duplicate coordinates merged
  by exact accumulation; convert between CSR and CSC; strictly sorted indices.
- **Zero modes**: `mode="eliminate"` drops entries whose accumulated value is
  zero; `mode="keep"` stores explicit zeros, distinguishable from missing
  coordinates via `explicit_at(i, j)`.
- **Zero-copy read-only views**: `matrix.T` reinterprets CSR buffers as CSC
  (and vice versa); `matrix[r0:r1, c0:c1]` builds a rectangular window without
  touching storage. Views are read-only and record the version they bind to.
- **Copy-on-write pages**: data arrays are split into fixed-size frozen pages.
  Point updates copy only the affected page; all other pages are shared with
  the previous version, so old views never observe new writes.
- **Transactions**: batch `set`, `insert_rows/cols`, `delete_rows/cols`.
  Commit applies operations sequentially to immutable versions; any failure
  aborts the whole batch and the base matrix stays byte-for-byte unchanged.
- **Serialization**: `dumps`/`loads` with a size/layout/mode header and a CRC32
  checksum per pointer/index/data block. `loads` validates every checksum and
  structural invariant before publishing a matrix; `dump_batch`/`load_batch`
  validate the whole batch before returning anything.
- **Dense oracle**: `DenseMatrix` is a tiny list-of-lists implementation used
  to cross-check randomized operation streams.
- **Storage statistics**: `sparsekit.STATS` reports pages created, pages copied
  on write, pages reused, views created and elements touched, demonstrating
  real buffer sharing and update complexity.

## Layout

- `sparsekit/pages.py` — `PagedBuffer`, pages, storage counters
- `sparsekit/matrix.py` — `SparseMatrix`, `SliceView`, `Transaction`
- `sparsekit/serialize.py` — checksummed binary format and batches
- `sparsekit/dense.py` — dense reference matrix
- `tests/test_sparsekit.py` — unittest suite
- `examples/demo.py` — runnable walk-through

## Run

```sh
python3.11 -m unittest discover -s tests -t . -v
python3.11 examples/demo.py
```

## Quick example

```python
from sparsekit import SparseMatrix, ELIMINATE, KEEP, dumps, loads

m = SparseMatrix.from_coo(
    (2, 2), [0, 0, 1], [0, 0, 1], [3, -3, 7], mode=ELIMINATE
)
assert m.nnz == 1                      # 3 + -3 cancels exactly
kept = SparseMatrix.from_coo(
    (2, 2), [0, 0], [0, 0], [3, -3], mode=KEEP
)
assert kept.explicit_at(0, 0) == (True, 0)

view = m.T                            # shares buffers, read-only
m2 = m.set(1, 1, 9)                   # copies one page; view stays on v0
restored = loads(dumps(m2))           # checksummed, validated before publish
```
