# Effective Time Between Two Instants

Computes the effective (open, non-closed) duration between two time points over
daily open windows and exception closed intervals, and inverts it (start +
effective duration -> end). Python 3.11, standard library only.

## Concepts (three distinct types, `timepoint.py`)

- `Duration` — a length of time, exact whole seconds (no floats anywhere).
- `Instant` — an absolute point in time (epoch seconds, UTC).
- `WallTime` — a local wall-clock reading with no offset attached.

`OffsetTable` is a synthetic UTC-offset transition table (stand-in for a tz
database). Conversions across a transition always reflect real elapsed time:
on a spring-forward day the 01:00-04:00 wall window really lasts 2 hours.
Wall-to-instant resolution: repeated wall readings (fall back) use the earliest
matching instant; skipped readings (spring forward) snap to the transition.

## Schedule (`schedule.py`)

- `DailyWindow` — daily open window in wall-clock time (overnight allowed).
- `ClosedInterval` — half-open exception interval `[start, end)` in instants;
  overlapping intervals are merged so overlaps are deducted only once.
- `effective_duration(schedule, start, end) -> Duration`
- `add_effective_duration(schedule, start, delta) -> Instant` — consumes only
  open time; a result landing inside a closed interval jumps to the next open
  segment.

## Run

```
python3.11 demo.py                  # sample scenarios
python3.11 -m unittest -v           # 30 tests
```

## Sample output (abridged)

```
== 1. Spring forward: open 01:00-04:00 wall, offset +1h at 02:00Z ==
  effective_duration(01:00, 04:00) on spring-forward day     2h00m00s
== 2. Fall back: open 01:00-04:00 wall, offset -1h at 02:00Z ==
  effective_duration(01:00, 04:00) on fall-back day          4h00m00s
== 3. Overlapping closed intervals are deducted only once ==
  effective_duration(01:00, 04:00) = 3h - 1.5h               1h30m00s
== 5. Inverse landing exactly on a closed interval jumps forward ==
  start 01:00 wall + 1h effective (raw end 02:00 is closed)  ... = wall 02:30:00
```

## Test result

```
Ran 30 tests in 0.018s
OK
```
