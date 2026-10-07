# local-utc-resolver

Local ↔ UTC resolution driven entirely by a caller-supplied **offset transition
table**. The system timezone is never consulted; the table is the complete
definition of the zone.

## Model

- Local civil time is represented as *local epoch milliseconds* — the wall
  clock reading interpreted via `Date.UTC(...)` (see `civilMs`).
- A `TransitionTable` is an initial offset (minutes east of UTC) plus a
  strictly ascending list of `{ utcMs, offsetMinutes }` transitions. Offsets
  must be whole minutes within ±24h (non-integer-hour zones like UTC+5:45 are
  fully supported).

## Semantics

- `localToUtc(table, localMs)` returns the UTC candidate(s):
  - **gap** (forward shift) → `{ kind: 'none' }`, the local time never occurs;
  - **fold** (backward shift) → `{ kind: 'ambiguous', earlierUtcMs, laterUtcMs }`;
  - otherwise → `{ kind: 'unique', utcMs }`.
- `resolveLocal(table, localMs, 'earlier' | 'later')` resolves to one instant,
  throwing `NonexistentLocalTimeError` in gaps and `AmbiguousLocalTimeError`
  in folds unless a disambiguation is supplied.
- `resolveInterval(table, startLocal, endLocal, { start, end })` resolves each
  endpoint independently, then validates the UTC pair has **strictly positive
  length** (`NonPositiveIntervalError`) — a wall-clock interval can invert
  across a fall-back depending on the choices.
- `enumerateUtcMapping(table, fromUtc, toUtc, step)` enumerates the UTC→local
  mapping for verification: folds show up as local ranges sampled twice, gaps
  as ranges never sampled.

## Test

```
bun test
```

Covers spring-forward (1h gap), fall-back (1h fold), non-integer-hour offsets
(UTC+5:45/+5:30 with 15-min fold & gap, Lord Howe style 30-min shifts), table
validation, interval resolution, and a UTC-mapping enumeration cross-check of
every transition.
