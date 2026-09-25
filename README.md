# tztable — synthetic offset-transition tables

Pure Python 3.11 (standard library only). Bidirectional conversion between
UTC instants and local wall-clock times driven by a synthetic, versioned
offset-transition table, with correct handling of fall-back overlaps,
spring-forward gaps, and arbitrarily many stacked transitions.

## Concepts

Three time concepts are distinct types and not interchangeable:

| Type       | Meaning                                             |
|------------|-----------------------------------------------------|
| `Instant`  | A point on the UTC timeline (ns since 1970-01-01Z). |
| `WallClock`| A local date + time-of-day, no offset attached.     |
| `Duration` | A fixed amount of elapsed time (nanoseconds).       |

All arithmetic uses Python integers — nanosecond precision is exact at
arbitrarily large epochs (no floats). Calendar stepping
(`plus_days`/`plus_months`/`plus_years`) follows the proleptic Gregorian
calendar and is deliberately different from adding a fixed `Duration`.

## Features

- **Bidirectional conversion.** `instant_to_wall` (unique, carries a `fold`
  identity) and `wall_to_instants` (all candidates with evidence: offset
  period, occurrence `fold`, human-readable reason).
- **Overlaps and gaps.** A wall time in a fall-back overlap yields 2+
  candidates (consecutive fall-backs can stack — never assume at most two);
  a wall time in a spring-forward gap yields none. `resolve_wall` supports
  `earlier`/`later`/`strict`/`compatible` policies; `earlier`/`later`
  round-trip through `fold` and preserve identity.
- **Interval mapping.** `map_wall_interval` maps a local interval to all
  candidate UTC intervals (one per offset period) plus `missing` reports
  for local segments inside a gap; `utc_union` merges everything.
- **Periodic rules.** `MonthlyRule` ("last Sunday of March at 02:00")
  expands by *calendar* stepping first, then disambiguates each transition
  against the table built so far — never fixed-duration stepping.
  Re-generation is deduplicated and idempotent.
- **Hot reload.** `TableRegistry` binds each table to an immutable version;
  publishing a new version makes it current without changing historical
  snapshots. `ConversionService` caches per version, so updates cause only
  local invalidation.

## Layout

```
tztable/
  core.py      # Instant / WallClock / Duration, calendar math
  table.py     # TransitionTable, candidates, interval mapping
  rules.py     # MonthlyRule, generate_transitions
  registry.py  # TableRegistry, ConversionService
examples/demo.py
tests/         # unittest suite (34 tests)
```

## Run

```sh
python3.11 -m unittest discover -s tests -v   # test suite
python3.11 examples/demo.py                    # runnable demo
```

## Example

```python
from tztable import Duration, Transition, TransitionTable, WallClock

table = TransitionTable(
    version="v1",
    base_offset=Duration.of(hours=2),
    transitions=(Transition(
        utc=WallClock.of(2024, 10, 27).to_instant_equiv(),  # 2024-10-27T00:00Z
        offset_after=Duration.of(hours=1),
    ),),
)

# Fall-back overlap: local 01:30 happens twice.
for c in table.wall_to_instants(WallClock.of(2024, 10, 27, 1, 30)):
    print(c.instant, c.fold, c.evidence)
# 2024-10-26T23:30Z fold=0 (UTC+02:00) / 2024-10-27T00:30Z fold=1 (UTC+01:00)

# Local interval inside the overlap -> two UTC intervals.
mapping = table.map_wall_interval(
    WallClock.of(2024, 10, 27, 1, 15), WallClock.of(2024, 10, 27, 1, 45))
assert len(mapping.candidates) == 2 and not mapping.missing
```
