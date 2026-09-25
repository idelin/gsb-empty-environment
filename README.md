# Sparse masked `A @ B`

Python 3.11, standard library only. The implementation maintains every currently
visible path contribution

\[
C_{ij} = \sum_{k: (k,j)\in M} A_{ik}B_{kj}
\]

without building the mask complement or a dense result.

## Components

- `masked_product/masked_product.py`: transactional engine.
- `demo_replay.py`: fixed, reproducible scenario showing cancellation, withdrawal,
  mask visibility and spill behavior.
- `tests/test_masked_product.py`: `unittest` suite, including a small hand-written
  dense reference and randomized mixed-update cross-checks.
- `TEST_RESULTS.md`: captured verification output.

## Guarantees

- Each batch contains sparse A/B/M adds, updates and deletes. Inputs change only
  after successful publication.
- Contribution storage is keyed by `(i, j, k)`. Each active `k`-path has one
  source record, so replacing a scalar cannot duplicate a source. Two paths may
  sum to zero while both remain withdrawable.
- Removing an M cell deletes visibility contributions but leaves A and B
  unchanged; adding M back recomputes and restores the paths.
- Intermediate contribution rows spill to SQLite after `row_threshold` entries;
  additional rows are spilled while in-memory contributions exceed
  `memory_contributions`.
- Publication stages spilled rows and the new sparse release in one SQLite
  transaction. A spill failure or user cancellation leaves the previously
  published version intact.
- A `Snapshot` is pinned to an explicit `(A_version, B_version, M_version)`
  vector. Concurrent readers therefore cannot mix rows from different
  transactions. Results are ordered by `(i, j)`.
- Dimensions and every index are supplied explicitly; NumPy/SciPy are not used.

## Run

```bash
python3.11 -m unittest -v tests.test_masked_product
python3.11 demo_replay.py
```
