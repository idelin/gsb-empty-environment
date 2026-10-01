# exact-ratio-metrics

Exact ratio, percentage-point and relative-change metrics in TypeScript,
built on BigInt rationals. Requires Node.js >= 23.6 (native TypeScript
type stripping; no build step, no dependencies).

## Rules of the house

- **Aggregate raw quantities first.** Sums of numerators and denominators
  are combined, never averages of per-record percentages:
  `1/2` merged with `9/10` is `10/12` (83.33%), not 70%.
- **Calibers are not mixable.** Every record carries a metric id plus a
  definition version (`MetricRef`); mixing calibers throws
  `CaliberMismatchError`.
- **Missing propagates, retractions exclude.** An unknown field poisons
  the ratio (`kind: 'missing'`); a retracted field (e.g. a withdrawn
  denominator) is excluded from sums, counted for audit, and does not
  poison.
- **Zero denominator != true zero.** A zero denominator sum is reported
  as `zero-denominator`; a zero numerator over a positive denominator is
  a true zero ratio (`value 0/1`).
- **pp != percent.** Percentage-point change is `(next - prev) * 100`
  (unit `pp`); relative change is `(next - prev) / prev * 100`
  (unit `percent`). Combining units throws `UnitMismatchError`. Relative
  growth from an old value of exactly 0 is undefined (`base-zero`).
- **BigInt everywhere.** No quantity passes through Number; rounding is
  a presentation-boundary concern and always names its mode
  (`half-up`, `half-even`, `floor`, `ceil`, `toward-zero`,
  `away-from-zero`).

## Layout

- `src/rational.ts` — exact BigInt rational arithmetic (normalized).
- `src/decimal.ts` — explicit-mode rounding to decimal/percent strings.
- `src/measure.ts` — observations, calibers, aggregation, ratio states.
- `src/changes.ts` — pp / relative changes with unit safety.
- `src/graph.ts` — expression DAG (source/sum/ratio/change) with exact
  per-record contributions and missing-event propagation.
- `src/simpson.ts` — Simpson reversal construction; overall rate
  decomposed exactly as `sum_g weight_g * rate_g`.
- `src/verify.ts` — reference checks: arbitrary slice merges,
  hierarchical merges and corrections must equal the direct aggregation
  of raw records.

## Run

```sh
npm test    # node --test test/*.test.ts
npm run demo
```

## Sample output

```
=== 1. sum raw quantities, never average percentages ===
sums: 10/12
ratio: 5/6 = 83.33%

=== 3. pp change vs relative change ===
pp change:    10.00 pp (unit: pp)
relative chg: 20.00% (unit: percent)
relative change from old value 0 -> {"kind":"undefined","unit":"percent","reason":"base-zero"}

=== 5. Simpson reversal (kidney stones) ===
arm A: overall 78.00% (small rate=93.10% weight=24.86% contrib=23.14% | large rate=73.00% weight=75.14% contrib=54.86%)
arm B: overall 82.57% (small rate=86.67% weight=77.14% contrib=66.86% | large rate=68.75% weight=22.86% contrib=15.71%)
group winners: {"small":"A","large":"A"} overall winner: B
reversal: true
```
