# Streaming batch table validator (Python 3.11, stdlib only)

Combined validation of batched columnar input: schema, row lengths,
validity bitmaps, dictionary indices, dictionary versions, shared-buffer
references and declared ordering (including **cross-batch** ordering).

* `batchval/model.py` — schema / batch / dictionary / error data model
  and bitmap helpers (null bit semantics, `null != NaN`).
* `batchval/validator.py` — streaming validator:
  * `validate_batch_structure()` — independent per-column structural
    checks; one bad offset cannot mask errors in another column.
  * `StreamValidator` — O(#columns) state: last sort key per ordered
    column, max dictionary version seen, dedup sets. Streams are never
    fully materialized.
* `batchval/checker.py` — independent naive/materializing reference
  checker; tests require its error set to equal the streaming result.
* `batchval/mutate.py` — single-point mutation generator over a known
  valid 3-batch table (one defect per mutant, plus a valid control).
* `tests/test_validator.py` — `unittest` suite.
* `demo.py` — runnable sample.

## Semantics

* Earliest bad row only per (column, row-level kind); other independent
  errors (other columns / kinds) are all reported.
* Evidence is a bounded slice (radius 2, at most 5 items) around the
  defect; dictionary access happens only after bounds checking, so a
  corrupt offset (e.g. `10**12`) never causes an unbounded read.
* `MISSING` (absent buffer/bitmap, unregistered dictionary version) is
  distinguished from `CORRUPT`; missing data only disables the checks
  that depend on it.
* Cross-batch state and global row numbers are deterministic for the
  logical row sequence: any chunking, and pause/resume via
  `snapshot()` / `StreamValidator(schema, registry, state=…)`, yield the
  identical error set.
* Columns declared sorted are checked across batch boundaries; columns
  not declared sorted are never treated as ordered.
* Integers use exact Python `int` comparison (no float coercion);
  `bool` is rejected. `NaN` is a legitimate float value, distinct from
  null (null comes only from the bitmap); NaN in a column declared
  ordered is an order violation.
* A column whose buffer is one row short of `declared_rows` is
  `LENGTH_MISMATCH`. Shared buffers must be both actually aliased
  (`is`) and declared.

## Run

```
python3.11 -m unittest discover -s tests -t . -v
python3.11 demo.py
```

## Sample output

### `demo.py`

```
--- damaged table, streamed batch by batch (no full materialization): 4 error(s) ---
  [corrupt] VALUE_TYPE_MISMATCH            batch=0 global_row=2 column=qty
           expected exact int, got float
           evidence=(0, (9, 1, 7.5, 2))
  [corrupt] DICT_INDEX_OUT_OF_RANGE        batch=1 global_row=6 column=city
           dictionary index 99 out of range (dictionary size 5)
           evidence=(0, (1, None, 99, 2))
  [corrupt] SORT_VIOLATION                 batch=1 global_row=4 column=id
           order inversion: 0 < previous key 3
           evidence=(3, 0)
  [corrupt] LENGTH_MISMATCH                batch=2 column=city
           column 'city': header length=4, buffer rows=3, batch rows=4

--- dict_index_oob: DICT_INDEX_OUT_OF_RANGE -> severity=corrupt
--- dict_index_negative: DICT_INDEX_OUT_OF_RANGE -> severity=corrupt
--- unknown_dict_version: DICT_VERSION_UNKNOWN -> severity=missing
--- missing_buffer: BUFFER_MISSING -> severity=missing

--- chunk invariance (same 12 rows, different chunking) ---
  chunks=[12]: 2 errors, identical=True
  chunks=[6, 6]: 2 errors, identical=True
  chunks=[4, 4, 4]: 2 errors, identical=True
  chunks=[1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1]: 2 errors, identical=True
  error set=[('DICT_INDEX_OUT_OF_RANGE', 'city', 6), ('SORT_VIOLATION', 'id', 5)]

--- pause/resume after batch 1 ---
  identical error set = True
```

### `python3.11 -m unittest discover -s tests -t .`

See `TEST_RESULTS.txt` (captured run, 16 tests, all OK).
