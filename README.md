# Layered Event-Count Engine

Python 3.11, standard library only. Exact event counting over non-uniform
business buckets mapped onto uniform fine buckets, with versioned snapshots,
late/corrected/retracted events, and count-index order statistics.

## Layout

- `treap.py` — persistent (path-copying) treap with subtree count/sum
  augmentation; every update costs O(log n) new nodes, old roots stay valid.
- `timemodel.py` — `WallClock` (exact `Fraction` seconds), `TimePoint`
  (integer synthetic ticks), `Duration` (ticks); `OffsetTable` converts
  wall clock <-> ticks piecewise and exactly.
- `buckets.py` — `BucketSchema`: uniform fine buckets (floor division, so
  `-1` lands in `[-10, 0)`, never `[0, 10)`) plus aligned non-uniform
  business boundaries; schemas are immutable and versioned.
- `engine.py` — `EventEngine`: versioned store with three persistent
  indexes (events by `(ticks, seq)`, bucket counts, id lookup).
- `test_engine.py` — 20 unittest cases. `demo.py` — runnable example.

## Key properties

- **Exact range counts**: `range_count(lo, hi)` partitions `[lo, hi)` into
  edge spans (accounted item by item) and fully-covered fine buckets (read
  from the bucket-count index); maximal runs tiling a business bucket are
  reported as business-layer contributions. Contributions are disjoint, so
  internal full buckets and edge details are never double counted, and each
  event lands in exactly one leaf bucket.
- **Provenance**: every `Contribution` carries its layer, span, source,
  data version and schema version.
- **Late / correct / retract**: `add` accepts out-of-order events;
  `correct` moves an event keeping its stable seq id; `retract` removes it.
  Each mutation commits a new immutable version; old snapshots stay
  queryable (`snapshot()` + `vid=`).
- **Order statistics**: `kth(k)` / `quantile(q)` run on the count index;
  duplicate timestamps are ordered by stable insertion id.
- **Schema updates**: `update_schema` registers new bucket rules and
  derives the bucket index once; event updates afterwards are incremental
  (no full-tree rebuild per update) and old snapshots keep their old schema.

## Run

```
python3.11 demo.py
python3.11 -m unittest test_engine -v
```

## Sample output (abridged)

```
== layered range count over [-15, 37) ==
  [ -15,  -10) edge      count=1 src=edge items of bucket -2 ... items=[(-15, 'e2')]
  [ -10,    0) fine      count=1 src=fine buckets [-1..-1] ...
  [   0,   20) business  count=5 src=business bucket [0, 20) ...
  [  30,   37) edge      count=1 src=edge items of bucket 3 ... items=[(33, 'e9')]
total (sum of disjoint contributions)                      8

== bucket rule update: old snapshots stay valid ==
snapshot before schema update                              7
same snapshot after update (unchanged)                     7
head with new schema v1                                    8
```

## Test results

```
Ran 20 tests in 0.290s
OK
```

Coverage includes: negative floor bucketing (`-1` in `[-10, 0)`), one leaf
bucket per event, item-by-item edge accounting, cross-layer double-count
checks, late/correction/retraction with snapshot validity, stable ordering
of duplicate timestamps, k-th/quantile accuracy, offset-table exact
round-trips, wall-clock/time-point/duration type separation, schema-update
snapshot isolation, and 600 randomized operations (add/correct/retract)
verified against a directly-sorted reference after every step.
