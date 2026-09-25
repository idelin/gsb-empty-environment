# batchval — streaming batch structural validator (Python 3.11, stdlib only)

Combined validation of typed column batches: **schema, length, validity
bitmap, dictionary index/version, and declared sort order** — across batch
boundaries, without materializing the table, without any query engine.

## Layout

- `batchval/model.py` — `TableSchema`, `Batch`, `Column`, `BufferRef`, `Error`, `Severity`
- `batchval/validator.py` — streaming `Validator` (`feed` / `snapshot` / `restore`) + `validate()`
- `batchval/oracle.py` — independent whole-table structural checker (test oracle)
- `batchval/mutate.py` — valid-table generator + single-point mutation operators
- `tests/test_validator.py` — unittest suite (unit + streaming + mutation fuzz)
- `demo.py` — runnable sample

## Semantics

- **Streaming**: `Validator.feed(batch)` keeps only the running sort extreme
  per declared column plus accumulated errors; input may be a generator.
- **Cross-batch order**: sort state carries over batch boundaries; only
  columns declared in `TableSchema.sorted` are order-checked — undeclared
  data is never treated as ordered.
- **Total order per column**: `NULL < values < NaN`. Null (bitmap bit 0) and
  NaN (valid float) are distinct. Integers compare exactly (arbitrary
  precision, int64 range enforced).
- **Missing vs corrupt**: unregistered dictionary versions, absent shared
  buffers, and undeclared `row_count` are reported as `Severity.MISSING`;
  out-of-range indices/offsets, short columns, bad bitmaps, and order
  inversions are `Severity.CORRUPT`.
- **Bounded reads**: out-of-range buffer references are clamped; a single
  bad offset can neither cause an unbounded read nor mask independent
  errors in other columns/checks.
- **Minimal evidence**: each error carries a small evidence tuple (e.g. the
  two rows of an inversion), never a whole column.
- **Pause/resume**: `snapshot()` is picklable; any chunking of the batch
  stream with suspend/resume yields the identical error multiset.

## Run sample (`python3 demo.py`)

```
== validation report: 6 errors ==
[corrupt] length       batch=1 col=score: column has 2 values, expected 3 evidence=(3, 2)
[corrupt] order        batch=1 row=3 col=id: asc order violated between rows 2 and 3 evidence=((2, '30'), (3, '25'))
[corrupt] dict_index   batch=1 row=4 col=label: dictionary index 99 out of range [0, 3) evidence=(99, 3)
[corrupt] buffer       batch=2 col=payload: buffer reference [1000000000000:1000000000002] out of bounds for buffer of size 6; read clamped evidence=(6, 1000000000000, 2)
[corrupt] length       batch=2 col=payload: column has 0 values, expected 2 evidence=(2, 0)
[missing] dict_version batch=2 col=label: dictionary 'labels' version 9 not registered evidence=('labels', 9)
earliest bad row: 3
corrupt: 5, missing-data: 1
pause/resume error set identical: True
```

## Tests (`python3 -m unittest discover -s tests -t . -v`)

14 tests, all passing:

- unit: cross-batch inversion, one-row-short column, undeclared-sort not
  enforced, null≠NaN ordering, exact int64 compare/range, missing dict
  version vs corruption, bounded bad offset not masking other errors,
  missing buffer, bitmap size, empty batch;
- streaming: lazy generator input with planted late error, identical error
  sets under arbitrary chunking + pickle pause/resume;
- mutation fuzz: 40 seeds × 7 single-point mutations (length, bitmap,
  dict index, sort, schema, dict version, huge buffer offset) — validator
  output must equal the independent oracle's error multiset, and each
  mutation's expected kind must surface.

```
Ran 14 tests in 0.183s

OK
```
