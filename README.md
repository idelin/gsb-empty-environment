# Synthetic Offset Time Conversion

Python 3.11 standard-library implementation. It models a time zone only through a synthetic, immutable transition table and does not use the host time-zone database.

## Type model

- `Instant`: an absolute UTC time point, stored as integer Unix nanoseconds.
- `WallClock`: a local calendar/time-of-day value with no offset.
- `Duration`: elapsed time in integer nanoseconds. It is never used in place of an instant or wall clock.
- `InstantInterval` / `WallInterval`: half-open `[start, end)` intervals.

Integer nanoseconds and proleptic Gregorian civil-date arithmetic keep examples exact, including values beyond normal platform or `datetime` ranges.

## Transition table

`TransitionTable(initial_offset, transitions, version=...)` is an immutable snapshot. Each `Transition` records:

- the UTC `Instant` where the offset changes;
- the `Duration` offset in effect at and after that instant;
- a stable transition identifier used in evidence and cache invalidation.

Result objects carry `table_version`. Replacing a `LiveTransitionTable` creates a new immutable snapshot and never mutates mapping results or snapshots returned from an earlier version.

## Bidirectional conversion

- `map_instant(instant, table)` maps one UTC instant to one wall clock.
- `map_utc_interval(...)` maps a UTC interval to ordered wall-clock pieces and reports wall-clock gaps skipped while UTC crosses a forward transition.
- `map_local_point(...)` returns every UTC candidate for a repeated wall clock, together with gap evidence for a non-existent wall clock.
- `resolve_local_point(..., policy="earlier" | "later")` preserves identity on round trip: `earlier` chooses the smallest UTC candidate and `later` chooses the largest.
- `map_local_interval(...)` returns every UTC interval candidate, the disjoint UTC union, the wall-clock portions actually covered, and half-open missing fragments for forward gaps.

The implementation scans every offset segment. Two consecutive fall-backs can therefore produce three candidates; it never assumes that a wall value has at most two UTC interpretations.

## Periodic rules

`daily`, `monthly`, and `yearly` first produce nominal wall-clock events by stepping the local calendar:

- daily steps one calendar day, even when that local day contains a 23-hour or 25-hour offset regime;
- monthly/yearly use calendar month/year arithmetic, not multiplication by 30 or 365 days;
- month-end rules can clamp to the available day or skip invalid dates such as 29 February in common years.

`build_transitions(initial_offset, schedules)` disambiguates generated nominal wall clocks only after calendar generation. Default is the earlier UTC candidate; `later` is available. Exact duplicate generated transitions are deduplicated, while simultaneous transitions with different offsets are rejected.

## Hot updates and caching

`LiveTransitionTable.current` exposes the latest immutable table and `snapshot(version)` returns historical tables. Its interval cache:

- reuses exact unaffected queries after an update;
- invalidates only the old/new local gap or repeated span when a transition with the same resulting offset is moved;
- invalidates from the earliest affected local time onward when a transition is added/removed or its resulting offset changes;
- clears if the initial offset changes.

Returned mappings remain frozen and retain their original version, so cached reuse only attaches a new version to an unaffected recomputation surface.

## Run

```sh
python3.11 examples/demo.py
python3.11 -m unittest -v
```

## Tests

The unittest suite covers:

- repeated local interval `[01:15, 01:45)` mapping to two UTC intervals;
- forward-gap missing fragments and skipped wall gaps from UTC intervals;
- earlier/later round-trip identity;
- interval union behavior, including touching UTC candidates;
- two consecutive fall-backs producing three point candidates;
- daily/monthly/yearly calendar stepping, month-end clamping, and leap-day skipping;
- duplicate periodic transition deduplication;
- versioned snapshots and partial cache invalidation;
- exact `10**30`-scale integer nanosecond round trips.
