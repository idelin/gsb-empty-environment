# Bitemporal Version Index

Python 3.11 library answering two kinds of questions over the same
append-only record store:

| Question | Axis swept | Axis fixed |
|---|---|---|
| "10时发生了什么" (what happened at 10?) | valid (business) time | observation time |
| "当时我们知道什么" (what did we know then?) | observation (transaction) time | valid time |

## Model

Every record carries **both** timestamps (`bitemporal/records.py`):

- `valid_from <= t < valid_to` — when the fact was true in the world.
  Intervals are uniformly **left-closed, right-open**; adjacent intervals
  `[0,10)` and `[10,20)` meet cleanly, the boundary belongs to the right one.
- `recorded_at` — when the record entered the system. A query with
  observation time `as_of` sees exactly the records with
  `recorded_at <= as_of`. Corrections, retractions and backfills are new
  immutable records; history is never rewritten.

`query(object_id, valid_time, as_of)` therefore locates evidence by
object and by both times.

## Overlap resolution (no arrival-order guessing)

Among records of one object that cover `valid_time` and are visible at
`as_of`, the winner is the maximum of the total order

```
(recorded_at, priority, record_id)
```

1. later-recorded knowledge supersedes earlier knowledge;
2. `priority` is an explicit caller-declared tie-break for records
   ingested at the same transaction time;
3. `record_id` (unique, monotonic) makes the order total.

The key depends only on record contents, so any insertion order yields
identical answers — verified by `test_priority_tiebreak_is_explicit_not_arrival_order`
and the shuffled-insertion property test. If the winning record is a
`RETRACT`, the object is known to have **no** value at that point
(`QueryResult.retracted`).

## Canonical scenario (fixtures.py)

Fact at valid time 10, recorded as `A` at tx 20, corrected to `B` at tx
30, corrected again to `C` at tx 35, retroactively retracted at tx 40,
re-asserted as `D` at tx 50:

| as_of | 15 | 25 | 32 | 37 | 45 | 55 |
|---|---|---|---|---|---|---|
| value at t=10 | — | A | B | C | (retracted) | D |

Fixtures also cover: adjacent intervals (`beta`), a future version that
must not leak into queries about t=10 (`gamma`), and a late backfill
invisible before its recording time (`delta`).

## Layout

- `bitemporal/records.py` — record model + resolution rule
- `bitemporal/index.py` — `BitemporalIndex` (per-object lists sorted by
  `valid_from`; point query bisects, then scans only covering candidates)
- `bitemporal/reference.py` — index-free full-scan reference query
- `fixtures.py` — synthetic fixtures above
- `demo.py` — both timeline demos (`python3.11 demo.py`)
- `tests/test_bitemporal.py` — contract tests + randomized property test:
  40 seeds x 60 random records x 300 random queries, index vs reference,
  with shuffled insertion (`python3.11 -m pytest`)

## Retention & reclamation

**Default: keep everything.** The store is append-only; any query with
`as_of` in the past may still need any old version, so old versions are
retained indefinitely unless a retention horizon is declared.

A version `V` becomes reclaimable only when **no admissible future query
can observe it as the winner**. Given a retention horizon `H` (the system
promises never to serve queries with `as_of < H`), a sufficient condition
for reclaiming `V` is:

> For every valid time `t` in `[V.valid_from, V.valid_to)` there exists a
> record `R` covering `t` with `V.recorded_at < R.recorded_at <= H` and
> `resolution_key(R) > resolution_key(V)`.

Then for every `as_of >= H`, `R` (or something even later) is visible and
outranks `V` at every point of `V`'s interval, so `V` can never win again
and may be physically deleted. Notes:

- Retraction tombstones are records too: a `RETRACT` recorded at `r` must
  be retained while queries with `as_of >= r` are admissible, because it
  *is* the winning answer ("known to have no value") for that window.
- Reclamation never changes query semantics for `as_of >= H`; queries
  older than `H` are exactly what the horizon gives up.
- Without a declared horizon (`H = -inf`), nothing is reclaimable — the
  safe default for audit-grade history.
