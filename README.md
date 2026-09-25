# bucketindex

Versioned event-count index over two-level time buckets. Python 3.11,
standard library only (`unittest` for tests).

## Features

- **Two-level bucketing**: non-uniform *business buckets* are mapped onto a
  fixed uniform *fine bucket* grid. Fine indices use floor division, so
  negative times behave correctly: tick `-1` falls in `[-10, 0)`, not
  `[0, 10)`.
- **Exact range counts** over arbitrary time ranges: complete internal
  fine buckets are answered from aggregate tree nodes, partial edge
  buckets are itemised event by event. The two parts are disjoint —
  internal buckets and edge details are never double counted, and each
  event lives in exactly one leaf bucket.
- **Late arrivals, corrections, retractions**: every mutation is recorded
  as a `Contribution(event_id, version, ticks, kind)`; `explain_range`
  traces any aggregate back to its contributing event versions.
- **Order statistics**: k-th event and quantile-time queries are served by
  the count index (persistent segment tree). Duplicate timestamps are
  ordered by stable event id.
- **Snapshots without rebuilds**: the segment tree is persistent
  (path-copying, O(log N) per update); every tree version stays queryable.
  Business-bucket rules are versioned separately in a `RuleRegistry` —
  updating the rules never touches or rebuilds the tree, and old rule
  snapshots keep answering queries.
- **Exact time model**: `WallClock` (clock reading), `Instant` (point on
  the synthetic tick axis) and `Duration` (ticks) are distinct types.
  Conversion goes through an `OffsetTable`, a synthetic offset translation
  table of monotone segments with exact rational rates.

## Layout

```
bucketindex/
  timeaxis.py    # WallClock / Instant / Duration / OffsetTable
  bucketing.py   # FineBuckets (floor division), BucketRule, RuleRegistry
  ptree.py       # persistent path-copying count tree + leaf item storage
  store.py       # EventStore: ingest/correct/retract, snapshots, queries
tests/           # unittest suite, incl. randomised differential test
examples/demo.py # end-to-end runnable sample
```

## Run

```
python3.11 -m unittest discover -s tests
python3.11 examples/demo.py
```

## Sample output (`examples/demo.py`)

```
=== 1. Exact time model: wall clock vs instant vs duration ===
wall 105 -> instant 1010 ticks (exact 1010)
instant 1010 -> wall 105
wall 1003/10 -> exact 5003/5 ticks, floored to instant 1000
duration between wall 90 and 110: 120 ticks (type: Duration)
wall(10) - wall(4) is a WallClockDelta, not a Duration

=== 2. Ingest events: late arrivals, correction, retraction ===
  ingest  e1 v1 @ tick 50
  ingest  e2 v1 @ tick 1002
  ingest  e3 v1 @ tick -1
  ingest  e4 v1 @ tick -25
  ingest  e5 v1 @ tick 3
  correct e2 v2 @ tick 77
  ingest  e6 v1 @ tick 3
  retract e4 v1 @ tick -25
live events: 5

=== 3. Exact range counts with itemised edges ===
count[-5, 100)   = 5 (internal 4 + edge items [-1])
count[-10, 80)   = 5 (aligned: internal only, 5)

=== 4. k-th event and quantiles (ties by stable id) ===
  k=0: (-1, 'e3', 1)
  k=1: (3, 'e5', 1)
  k=2: (3, 'e6', 1)
  k=3: (50, 'e1', 1)
  k=4: (77, 'e2', 2)
median time: Instant(ticks=3)

=== 5. Versioned business rules; old snapshots stay valid ===
rule v1 (0, 50): [1, 2, 2]
rule v2 (-10, 40, 80): [0, 3, 2, 0]
rule v1 after update (snapshot): [1, 2, 2]
tree rebuilt on rule change? False

=== 6. Contribution lineage ===
explain[0, 100): [(3, 'e5', 1), (3, 'e6', 1), (50, 'e1', 1), (77, 'e2', 2)]
```

## Test results

```
$ python3.11 -m unittest discover -s tests
................................
----------------------------------------------------------------------
Ran 32 tests in 7.187s

OK
```

The suite covers: exact time conversions and type separation, negative-time
floor bucketing (`-1` in `[-10, 0)`), non-uniform business bucket mapping,
edge itemisation vs internal aggregates (no double counting), single-leaf
guarantee under correction, retraction, contribution lineage, k-th/quantile
with stable-id tie-breaking, tree and rule snapshot validity, and a
randomised differential test (random ingest/correct/retract traces across
multiple seeds checked against a direct-sorted brute-force reference).
