# bigint-base-convert

Arbitrary-base (2–62) conversion for big integers and exact rationals, in
TypeScript on **BigInt only** — no `Number` intermediates anywhere in the
numeric pipeline. Runs on Node.js ≥ 23.6 (native type stripping, no build
step, no dependencies).

```sh
npm test    # node --test (built-in runner)
npm run demo
```

## Numeral grammar

```
numeral := [-] int [ "." frac [ "(" cycle ")" ] ]
```

- `0.1` terminating, `0.(3)` pure cycle, `0.1(6)` prefix + cycle.
- `frac` may be empty only when a cycle follows: `0.(3)` legal, `1.` not.
- Digits are matched **case-sensitively** against an explicit charset.

## Charsets (explicit, case-sensitive)

Named presets: `lower` (`0-9a-z`, bases 2–36), `upper` (`0-9A-Z`),
`base62` (`0-9A-Za-z`), `base62alt` (`0-9a-zA-Z`). Any explicit string of
≥ `base` unique characters also works; uniqueness is checked over the whole
string, and `- + . ( )` are reserved. Wrong case is an invalid digit, not a
different spelling.

## Rules (all explicit)

- **Negative zero**: `-0`, `-0.0`, `-0.(0)` normalize to canonical `0/1`;
  a minus sign is emitted only for values `< 0`. `-0` is never rendered.
- **Invalid digits**: `InvalidDigitError` carries `position` (index in the
  whole numeral, sign/dot/parens counted), `char`, and `base`.
- **Cycle detection**: remainders are bounded by the denominator, so every
  expansion terminates or repeats within `den` steps — `0.1` decimal to
  binary yields `0.0(0011)` and **stops**; it never streams forever.
- **Output budget**: `budget` caps emitted fractional digits. Exhaustion
  before termination/cycle yields `status: "budget-exhausted"` and
  `complete: false` — never silently final. Termination/cycle checks do
  not consume budget (they emit nothing).
- **Rounding** (only on budget-truncated output): `truncate` (default)
  drops the tail; `nearest` peeks one digit past the cut and rounds the
  magnitude half-up (`2 * nextDigit >= base`), carry may propagate into
  the integer part (`9.999` budget 2 → `10.00`).
- **Chunked integer conversion**: digits are emitted by dividing once per
  chunk of `k` digits by `base^k` (largest `k` with `base^k ≤ 2^53`),
  not once per digit by the small base — this avoids the O(n²)
  degradation of naive per-digit division, and the report proves it
  (500-digit decimal → hex uses 32 divisions, not 500).
- **Pausable output**: `FractionExpander` supports `run(n)` / `pause()` /
  `resume()` and JSON `snapshot()` / `restore()` for cross-process resume.
- **Independent verification**: `verifyFractionDigits` checks a claimed
  `(prefix, cycle)` in closed form — `P/b^p + C/(b^p(b^c−1))` — without
  re-running the expansion, and also flags non-minimal cycles and
  non-canonical starts.

## Report

Every `formatRational` / `convert` returns a workload + quota report:

```
{ outputBase, integerDivisions, fractionSteps, budget, budgetUsed,
  digitsEmitted, status, complete, rounded, rounding }
```

## Layout

- `src/charset.ts` — charset presets + validation
- `src/rational.ts` — exact BigInt rational, negative-zero normalization
- `src/integer.ts` — chunked big-integer ⇄ digits, workload counters
- `src/fraction.ts` — expansion, budget, pause/resume, verification
- `src/convert.ts` — numeral parse/format/convert + report
- `test/` — `node:test` suites incl. an exhaustive small-rational
  round-trip grid (±12/1..12 × 10 base/charset combos, 2500 cases)
- `examples/demo.ts` — runnable samples
