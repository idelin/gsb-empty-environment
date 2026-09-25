# Half-open interval joins

Python 3.11, standard library only, no query engine or third-party package.

## Data model

Each input table is one `TypedBatch` or a list of batches. A batch contains:

- `columns`: column name to one of `INT`, `FLOAT`, `STRING`, or `BOOL`.
- `rows`: dictionaries validated against those column types.

`None` is SQL `NULL` and is allowed in every column. `float("nan")` is a value,
not NULL, and is valid only in a `FLOAT` column. Type checks are strict: `1` is
not accepted in a `FLOAT` column and `True` is not accepted as an integer.

Intervals use the half-open form `[start, end)`:

- Point match: `start <= point < end`.
- Interval overlap: `max(start1, start2) < min(end1, end2)`.
- Overlap length: `min(end1, end2) - max(start1, start2)`.
- Point matches report overlap length `0`.
- `start >= end`, NULL endpoints, and NaN endpoints produce no interval.
- Duplicate endpoints, duplicate intervals, and nested intervals are retained.

When both endpoints are integers, subtraction stays in Python arbitrary-precision
integer arithmetic.

## API

```python
from interval_join import TypedBatch, point_join, interval_join

points = TypedBatch(
    {"id": "STRING", "point": "INT"},
    [{"id": "p5", "point": 5}],
)
intervals = TypedBatch(
    {"id": "STRING", "start": "INT", "end": "INT"},
    [{"id": "a", "start": 0, "end": 5}, {"id": "b", "start": 5, "end": 8}],
)

point_join(points, intervals)
# p5 matches only b: point 5 is outside [0,5) and inside [5,8).

interval_join(
    TypedBatch({"id": "STRING", "start": "INT", "end": "INT"},
               [{"id": "A", "start": 0, "end": 7}]),
    TypedBatch({"id": "STRING", "start": "INT", "end": "INT"},
               [{"id": "X", "start": 3, "end": 5}]),
)
# IntervalMatch(left_id="A", right_id="X", overlap_length=2)
```

Column names can be overridden with keyword arguments such as `point_col`,
`start_col`, `end_col`, and ID column names.

## Index

`IntervalIndex` and `IndexedInterval` are exported for direct use. The index is
a centered interval tree:

- Build: `O(n log n)` time and `O(n)` space.
- Point query: `O(log n + matches)`.
- Interval query: `O(log n + matches)` on each root-to-boundary path.
- Non-overlapping Cartesian pairs are not examined.

When many nested intervals all overlap one another, the output itself can be
quadratic; that cost is unavoidable and proportional to returned matches.

## Run

```sh
python3.11 demo.py
python3.11 -m unittest discover -s tests -v
```

## Verified sample

The included demo prints:

```text
point-join: point 5 belongs to [5,8), not [0,5)
  point_id= 1.0 interval_id=11.0 overlap=0
  point_id= 6.0 interval_id=11.0 overlap=0
  point_id=NULL interval_id=10.0 overlap=0
interval-join: [0,7) x [3,5) has overlap length 2
  left_id=A right_id=X overlap=2
  left_id=B right_id=X overlap=2
```

The NULL-ID point at 4 matches `[0,5)`. The NaN-ID point is skipped because its
position is NULL. Zero-length and NULL/NaN endpoint intervals have no match.

Test result on Python 3.11.15:

```text
Ran 9 tests in 0.000s

OK
```
