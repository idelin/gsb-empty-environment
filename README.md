# utc-work-calendar

Accumulate working durations over a set of UTC open intervals, with pause days.

## Semantics

- All timestamps are epoch milliseconds; intervals are half-open `[start, end)`.
- Open intervals may overlap — they are **unioned first**, so shared time is
  never counted twice (`unionIntervals`).
- A pause day (`'YYYY-MM-DD'`, UTC) **covers all open time of that day**; the
  whole UTC day is carved out of the open set (`subtractPauseDays`).
- Finishing **exactly on the right endpoint** of an open interval is a legal
  completion (likewise, backward deduction may land exactly on a left
  endpoint).
- Negative durations deduct **backward** in time from the cursor.

## API

```ts
import { accumulate } from './src/calendar.ts';

const result = accumulate(
  [
    { start: Date.parse('2026-01-05T09:00:00Z'), end: Date.parse('2026-01-05T17:00:00Z') },
    { start: Date.parse('2026-01-06T09:00:00Z'), end: Date.parse('2026-01-06T17:00:00Z') },
  ],
  Date.parse('2026-01-05T10:00:00Z'), // cursor
  14 * 60 * 60 * 1000,                // duration (negative = backward)
  ['2026-01-06'],                     // pause days
);
```

`accumulate` returns:

| field           | meaning                                             |
| --------------- | --------------------------------------------------- |
| `end`           | final cursor position                               |
| `complete`      | full duration was accumulated                       |
| `accumulatedMs` | open time consumed                                  |
| `remainingMs`   | unfulfilled remainder (0 when complete)             |
| `evidence`      | traversed open sub-intervals, chronological, disjoint |

The total length of `evidence` always equals `accumulatedMs`, and every
evidence interval lies inside the effective (unioned, pause-subtracted)
calendar.

## Verification

`test/calendar.test.ts` builds a **minute-by-minute finite calendar** as an
independent oracle: each minute slot is open iff it lies fully inside the
effective calendar. Directed scenarios (cross-day accumulation, full pause
days, exact-endpoint completion, negative backward deduction, insufficient
time) plus 400 seeded randomized calendars are checked against it — comparing
`end`, `complete`, `accumulatedMs`, and the exact minute coverage of
`evidence`.

## Requirements & running

No dependencies. Requires Node >= 22.6 (native TypeScript type stripping):

```sh
node --test test/calendar.test.ts
```
