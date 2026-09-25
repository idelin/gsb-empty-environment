# merge-agg

Merge-aggregation over multiple sorted input streams, with checkpointing
and recovery.  Pure Python 3.11 standard library; no query engines, no
third-party dependencies.

## What it does

* **K-way merge aggregation** — consumes rows in non-decreasing key order
  from N input streams, groups by key, and folds each group with
  `sum` / `count` / `min` / `max` / `avg` aggregates.
* **Checkpoint & recovery** — `save_checkpoint()` persists the incomplete
  group's partial aggregates plus one cursor per input stream;
  `MergeAggregator.restore()` resumes exactly where the run paused.
  Groups committed before the checkpoint are never emitted again, because
  the cursors position every stream past the rows that produced them.
* **Temporary emptiness is not EOF** — a stream whose `poll()` returns
  `None` is merely dry for now; the merger pauses (keeping the current
  group open) instead of finalising results prematurely.  Only
  `close()` marks permanent end-of-input.
* **Cursor versioning** — each cursor records the stream's data
  `version`.  Restoring onto a stream with a different version raises
  `CursorVersionMismatch`, since positions would be meaningless against
  regenerated data.

## Data model

* Tables arrive as `Batch` objects validated against a typed `Schema`
  (`int`, `float`, `str`, `bool` columns).
* **null vs NaN** — `None` is null (skipped by every aggregate); `NaN`
  is a real float value: counted by `count`, propagated by
  `sum`/`min`/`max`/`avg`.
* **Exact integers** — `int` columns use Python's arbitrary-precision
  ints; `avg` over ints returns an exact `fractions.Fraction`.
* An aggregate over zero non-null values yields `None` (SQL-style),
  except `count`, which yields `0`.

## Layout

```
merge_agg/
  batch.py       # Schema + Batch, type validation, null/NaN helpers
  stream.py      # ListStream (appendable, seekable), Cursor
  aggregate.py   # per-group Accumulator with exact int arithmetic
  merger.py      # MergeAggregator: run/pause, checkpoint, restore
examples/demo.py # end-to-end pause/checkpoint/resume demonstration
tests/           # unittest suite
```

## Usage sketch

```python
from merge_agg import Batch, ListStream, MergeAggregator, Schema

schema = Schema([("k", "int"), ("v", "int")])
s1 = ListStream("s1", schema, [Batch(schema, [(1, 10), (2, 1)])], version=1)
s2 = ListStream("s2", schema, [Batch(schema, [(1, 20)])], version=1)
s1.close(); s2.close()

merger = MergeAggregator([s1, s2], key_column="k",
                         aggregations=[("sum", "v"), ("count", "v")])
sink = []
status = merger.run(sink)          # "complete" or "paused"
merger.save_checkpoint("ckpt.pkl") # safe at any pause
# later: MergeAggregator.restore("ckpt.pkl", equivalent_streams)
```

## Run the demo

```
$ python3.11 examples/demo.py
=== Phase 1: partial input, group 2 spans three streams ===
status: paused  (stream 'b' ran dry mid-group; temporary emptiness is not EOF)
committed so far:
  key=1  sum(v)=600  count(v)=3
checkpoint saved to .../checkpoint.pkl
  incomplete group key: 2, partial sum: 11
  cursors (rows consumed): {'a': 2, 'b': 2, 'c': 1}

=== Phase 2: restore from checkpoint with the full streams ===
status: complete
final output (group 1 committed once, never re-emitted):
  key=1  sum(v)=600  count(v)=3
  key=2  sum(v)=131  count(v)=4
  key=3  sum(v)=5  count(v)=1
  key=4  sum(v)=7  count(v)=1

=== Baseline: same data in one uninterrupted run ===
baseline output:
  key=1  sum(v)=600  count(v)=3
  key=2  sum(v)=131  count(v)=4
  key=3  sum(v)=5  count(v)=1
  key=4  sum(v)=7  count(v)=1
pause/resume output is identical to the uninterrupted run

=== Cursor version mismatch ===
raised CursorVersionMismatch: stream 'a': cursor version 1 does not match stream version 2
```

## Run the tests

```
$ python3.11 -m unittest discover -s tests -v
...
Ran 18 tests in 0.003s

OK
```

The suite covers: schema/type validation, null-vs-NaN semantics, exact
integer arithmetic (values beyond 2**70, exact `Fraction` averages),
three-stream merge ordering, unsorted/null/NaN-key rejection,
temporary-empty vs permanent EOF, mid-group pause across three streams
with checkpoint/restore equivalence to an uninterrupted run, no
re-emission of committed groups after restore, and cursor
version-mismatch errors.
