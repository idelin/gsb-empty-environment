# Bitemporal Rectangle Index (Python 3.11, stdlib only)

A bitemporal store: every record carries a **valid-time** interval
`[valid_from, valid_to)` (business time, `valid_to=None` = open ended) and a
**recorded-version** span `[version_from, version_to)` (observation version,
`None` = still current). Corrections, retractions and priority changes are
applied as atomic, monotonically increasing versions; history is never
overwritten.

## Layout

- `bitemporal/timekit.py` — exact time primitives: `Duration`, `TimePoint`
  (synthetic timeline), `WallClockTime` (UTC timeline) are three distinct,
  non-mixable types, all stored as exact `Fraction` microseconds.
  `SyntheticOffsetTable` converts between the two timelines via anchor
  points + a fixed rate, with exact round-trips.
- `bitemporal/rectindex.py` — the rectangle index. The valid-time axis is
  cut into elementary contiguous segments; each segment holds an append-only
  list of version entries tiling `[0, +inf)`. Point query = two bisections;
  range query walks only intersecting rectangles. A `probes` counter proves
  queries never scan all history.
- `bitemporal/store.py` — `BitemporalStore` with atomic `transact([...])`
  (`Upsert` / `Retract` / `ChangePriority`), point query `value_at(version,
  t)`, 2-D range `scan(...)`, provenance `explain(version, t)` and
  `history_of(record_id)`.
- `bitemporal/verify.py` — independent verifier: replays the op log with a
  naive model and, per version, checks coverage, boundary (half-open, no
  double-hit) and history invariance against the index.
- `demo.py` — runnable sample. `tests/` — unittest suite (50 tests).

## Semantics

- **Visibility**: at any (version, time) point the covering record with the
  highest explicit `priority` wins; ties break deterministically on the
  larger `record_id`. The store materialises disjoint canonical visible
  fragments (adjacent identical fragments are merged).
- **Retroactive correction**: one correction may split many rectangles —
  every visible/raw rectangle overlapping the corrected region is closed at
  the new version and replaced. Earlier observation versions are immutable.
- **Retraction** closes rectangles (`version_to = v`) instead of deleting
  them; `history_of` still returns them.
- **Atomicity**: a batch validates fully before mutating; any failure leaves
  version, indices and history untouched.
- **Boundaries**: intervals are half-open `[from, to)`; a point exactly on a
  boundary hits only the interval starting there. Open-ended (`None`)
  intervals are split by later corrections like any other.

## Run

```bash
python3 demo.py                          # runnable sample
python3 -m unittest discover -s tests -v # test suite
```

## Sample output (abridged)

```
2. Versioned corrections over a price record (EUR/USD, synthetic ticks)
  versions: insert A=v1, insert B=v2, fix A=v3, prio A=v4, retract B=v5
  version | t=1   | t=3   | t=5   | t=7   | t=9   | t=12
  v1       |A=100 |A=100 |A=100 |A=100 |A=100 |-
  v2       |A=100 |A=100 |B=200 |B=200 |B=200 |B=200
  v3       |-    |A=150 |B=200 |B=200 |B=200 |B=200
  v4       |-    |A=150 |A=150 |A=150 |B=200 |B=200
  v5       |-    |A=150 |A=150 |A=150 |-    |-

4. Provenance: why is this value visible at v2, t=5?
  winner ->                                      px/B value=200
  contender px/A (prio 0) ->                     shadowed: priority 0 < 1

6. Independent verification (naive replay vs index)
  versions checked -> 6   sample points checked -> 66   result -> OK

7. Query cost: probes vs history size
  rectangles in index -> 306   probes for one point query -> 2
```

## Test results

```
$ python3 -m unittest discover -s tests
Ran 50 tests in 0.182s
OK
```

Coverage of the suite: exact fractional time arithmetic and cross-type
rejection; offset-table round-trips (multiple anchors, fractional rates);
per-version point queries; boundary no-double-hit; retroactive corrections
splitting multiple rectangles while earlier versions stay unchanged;
open-ended intervals split by later corrections; retractions preserving
history; explicit priority + deterministic tie-break; atomic batches
(including all-or-nothing failure); clipped 2-D range scans with disjoint
fragments; provenance explanations; sub-linear query probes; a seeded
randomised workload checked by the independent verifier; and tamper
detection by the verifier.
