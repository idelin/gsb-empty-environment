# offset-table-tz

Local <-> UTC conversion driven entirely by an explicit offset transition
table — the system timezone is never consulted. Offsets are integer minutes,
so non-integer-hour zones (+05:45, +10:30) work natively.

## Model

```ts
interface ZoneTable {
  name: string;
  initialOffsetMinutes: number;        // offset before the first transition
  transitions: { utc: number; offsetMinutes: number }[]; // ascending by utc
}
```

Instants and local wall-clock readings are both epoch-like milliseconds;
`local = utc + offsetMinutes * 60_000`.

## API

- `offsetAtUtc(table, utc)` / `utcToLocal(table, utc)` — UTC -> local.
- `localToUtcCandidates(table, local)` — all UTC instants matching a local
  reading: `[]` in a gap (spring forward), one normally, two in a fold
  (fall back).
- `classifyLocal(table, local)` — `{kind: "none" | "single" | "ambiguous"}`.
- `resolveLocal(table, local, foldChoice?)` — single UTC instant; throws
  `NonExistentLocalTimeError` in a gap and `AmbiguousLocalTimeError` in a
  fold unless `"earlier"` / `"later"` is given.
- `localIntervalToUtc(table, startLocal, endLocal, {startFoldChoice,
  endFoldChoice})` — resolves each endpoint independently and requires a
  strictly positive UTC duration (`NonPositiveIntervalError` otherwise).

## Verification

`npm test` builds with `tsc` and runs `test/enumeration.test.ts`, which
enumerates UTC instants minute-by-minute across every transition and checks
the UTC -> local -> candidates round trip, plus exact gap/fold sizes for
one-hour, half-hour (+10:30/+11:00), and quarter-hour (+05:30/+05:45) shifts.
