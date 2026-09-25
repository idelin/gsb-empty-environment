# unit-migration

Schema-driven unit migration for JSON-compatible trees, with exact rational
arithmetic. Written in TypeScript, tested with the Node.js built-in test
runner (`node:test`). No runtime dependencies.

## Requirements

- Node.js >= 22.6 (uses native type stripping; developed on v23.9).
  Run with `node --experimental-strip-types` on 22.6–22.17; no flag needed later.
- No `npm install` needed — there are zero dependencies.

## Run

```sh
npm test     # node --test test/*.test.ts
npm run demo # node examples/demo.ts
```

## Model

- **Rational** (`src/rational.ts`): exact bigint rationals. Decimals parse
  exactly (`0.1` is `1/10`, not a float approximation). Rounding to integer
  is always explicit: `trunc | floor | ceil | half-up | half-even`.
- **Units** (`src/units.ts`): every unit maps affinely to its dimension's
  canonical base: `base = scale * value + offset`. Each unit has a
  `dimension` and a quantity `kind`:
  - `absolute` — a reading on a scale, e.g. `degC` (offset 273.15);
  - `difference` — a delta, e.g. `dC` (offset must be zero).
  Kinds must match exactly; `20 degC -> 293.15 K` but `20 dC -> 20 dK`.
- **Schema** (`src/schema.ts`): describes the JSON tree. Object keys are
  unordered, arrays are ordered. Numeric leaves declare their unit and may
  declare `integer: true`, which requires an explicit rounding mode.
- **Compilation** (`src/compile.ts`): rules bound to the same path form an
  ordered multi-step chain. Compile-time checks:
  - path consistency: every rule path must resolve to a numeric leaf;
  - per-step dimension and quantity-kind compatibility;
  - contradictory conversion cycles: if a chain revisits a named unit, the
    affine transform composed over the cycle must be the identity;
  - integer targets must have an explicit rounding mode.
- **Migration** (`src/migrate.ts`):
  - builds a new tree; the input is never mutated;
  - all arithmetic is exact rational, rounding is deferred to the end of
    each chain;
  - integer targets emit an exact rounding-error interval in the audit —
    loss is recorded, never silently truncated;
  - cross-field constraints are validated on the complete new object; any
    violation rolls the whole migration back (`ok: false`, original data);
  - `invertMigration` returns inverse rules with `lossless: false` — the
    inverse never claims to be lossless;
  - `analyzeChain` verifies multi-step conversions against an exact
    rational reference, exposing deferred-vs-eager rounding differences
    and float64 pipeline error.
- **Errors** (`src/errors.ts`): every error carries a machine-readable
  `code` and a concrete path (e.g. `readings[1].temp`).

## Layout

```
src/rational.ts   exact rational arithmetic + explicit rounding modes
src/units.ts      unit registry: dimension, affine offset, quantity kind
src/schema.ts     JSON-tree schema, path patterns, leaf resolution
src/compile.ts    rule compilation: paths, chains, contradiction cycles
src/migrate.ts    execution, audit, constraints/rollback, inverse, verify
src/errors.ts     MigrationError with code + path
test/             node:test suites (32 tests)
examples/demo.ts  runnable end-to-end sample
```

## Example

```ts
import { migrate } from './src/migrate.ts';

const result = migrate(
  {
    type: 'object',
    fields: {
      altitudeM: { type: 'number', unit: 'm', integer: true, rounding: 'half-even' },
      readings: {
        type: 'array',
        items: {
          type: 'object',
          fields: {
            tempC: { type: 'number', unit: 'degC' }, // absolute
            spreadC: { type: 'number', unit: 'dC' }, // difference
          },
        },
      },
    },
  },
  [
    { path: 'readings[].tempC', to: 'K' },
    { path: 'readings[].spreadC', to: 'dF' },
    { path: 'altitudeM', to: 'mm' }, // 1 m -> exactly 1000 mm
  ],
  { altitudeM: 12, readings: [{ tempC: 20, spreadC: 9 }] },
);
// result.data.readings[0].tempC  === 293.15   (offset applied)
// result.data.readings[0].spreadC === 16.2    (no offset)
// result.data.altitudeM === 12000             (exact, errorInterval [0, 0])
```
