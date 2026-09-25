# Composable Row-Selection Bitmap (Python 3.11, stdlib only)

A small columnar selection engine built from scratch — no query engines,
no third-party dependencies.

## Layout

- `bitmap.py` — compressed bitmap: roaring-style 4096-bit segments, each
  stored as `sparse` (sorted offset list, ≤ 256 bits set) or `dense`
  (64-bit word array). Supports `&` (AND), `|` (OR), `~` (NOT), `rank(i)`
  (inclusive popcount) and 1-based `select(k)`.
- `table.py` — typed record batches (`INT` / `FLOAT` / `STRING` / `BOOL`),
  column expressions and predicates that evaluate to bitmaps.
- `demo.py` — runnable sample. `test_bitmap.py` / `test_table.py` —
  unittest suites.

## Guarantees

- **Length checking**: combining bitmaps of different lengths raises
  `BitmapLengthError` (also enforced by `Table.filter`).
- **NOT stays in range**: `~bm` flips only `[0, len)`; tail padding bits
  in the final segment are masked and never leak (white-box tested).
- **rank/select**: `select(rank(i)) == i` for every selected `i`;
  `rank` clamps outside the row range; `select` raises `IndexError`
  out of bounds; empty bitmaps behave correctly throughout.
- **null ≠ NaN**: null (`None`) never matches a comparison; NaN follows
  IEEE rules and is matched by `is_nan()` only. `is_null()` matches
  `None` only.
- **Exact integers**: `INT` columns use arbitrary-precision Python ints,
  so `2**62` and `2**62 + 1` stay distinct through arithmetic.

## Usage

```python
from table import Table, ColType

t = Table({"id": ColType.INT, "temp": ColType.FLOAT, "city": ColType.STRING})
t.add_batch({"id": [1, 2], "temp": [21.5, float("nan")],
             "city": ["beijing", None]})
t.add_batch({"id": [3], "temp": [35.0], "city": ["beijing"]})

pred = (t.col("city") == "beijing") & ~(t.col("temp") < 30.0)
bm = t.where(pred)          # Bitmap(len=3, count=1)
rows = t.filter(pred)       # [{'id': 3, 'temp': 35.0, 'city': 'beijing'}]
bm.select(bm.rank(2))       # -> 2
```

## Run sample

```
$ python3.11 demo.py
== typed batches ==
ingested 6 rows in 2 batches

== null vs NaN ==
null rows: [2]  nan rows: [1]
null & nan overlap: 0 (must be 0)

== composable selection ==
(city == 'beijing') & ~(temp < 30) -> Bitmap(len=6, count=1) rows [3]
  {'id': 4, 'temp': 35.0, 'city': 'beijing'}

== rank/select roundtrip ==
selection rows: [0, 3, 5]
  rank(0) = 1  select(1) = 0
  rank(3) = 2  select(2) = 3
  rank(5) = 3  select(3) = 5

== exact integer arithmetic ==
n*2 == 2**63 + 2 selects [1] (floats would merge 2**62 and 2**62+1)

== compressed segments (sparse + dense) ==
segments: ['dense', 'sparse', 'empty']  count=2050
NOT: count=6242 of length 8292; max index 8291 < 8292 (no tail-bit leak)

== length mismatch ==
BitmapLengthError: cannot combine bitmaps of length 4 and 5
BitmapLengthError: selection bitmap has length 3 but table has 6 rows
```

## Test results

```
$ python3.11 -m unittest discover -v
... (27 tests: AND/OR/NOT, length-mismatch errors, tail-padding leaks,
rank/select roundtrips, empty-bitmap boundaries, sparse/dense segment
storage, random set-model cross-checks, batch type validation, null vs
NaN, exact big-int arithmetic, predicate/bitmap interop) ...
----------------------------------------------------------------------
Ran 27 tests in 2.175s

OK
```
