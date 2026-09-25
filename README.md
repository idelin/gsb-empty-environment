# unit-migrate

Schema-driven unit migration for JSON-compatible trees, with exact rational
arithmetic, affine unit definitions, an audit trail for every converted
value, cross-field constraints with atomic rollback, and honest inverse
migrations. Pure TypeScript, zero dependencies, tested with the Node.js
built-in test runner.

## Requirements

Node.js >= 23.6 (runs TypeScript natively) or >= 22.6 (via
`--experimental-strip-types`). `npm test` / `npm run sample` use
`tools/with-node.mjs`, which locates a suitable runtime automatically.

```sh
npm test         # node --test "test/*.test.ts"
npm run sample   # examples/sample.ts
```

## Concepts

**Exact rationals.** Every conversion is computed with bigint rationals
(`src/rational.ts`). Numeric strings (`"0.5004"`, `"22/7"`) parse exactly;
JSON numbers convert via their shortest round-trip decimal. Nothing is ever
silently truncated.

**Units carry dimension, affine offset and quantity kind**
(`src/units.ts`). A conversion declares `to = from * ratio + offset` within
one dimension. Whether the offset applies depends on the *quantity kind* of
the field: `absolute` quantities use the full affine map (20 °C = 293.15 K),
`delta` quantities only scale (Δ20 °C = Δ20 K = Δ36 °F).

**Compile checks first.** Building a `UnitRegistry` verifies that every
dimension is connected, every ratio invertible, and every redundant
conversion consistent — a contradictory conversion cycle
(e.g. m→cm=100, cm→mm=10, m→mm=999) fails with `COMPILE_CYCLE` and lists
the units involved. `compilePlan` (`src/plan.ts`) checks path consistency:
invalid patterns, duplicates, and prefix conflicts (`a` vs `a.b` — a node
cannot be both a numeric field and a container) are `COMPILE_PATH` errors.

**Explicit integer precision.** A field rule declares
`precision: { type: 'integer', rounding: 'half-up' | 'half-even' | 'floor' | 'ceil' | 'toward-zero' }`.
The audit entry records the exact rational result, the exact signed error,
and the worst-case error interval for the strategy
(e.g. half-even → `[-0.5, +0.5]`, floor → `[0, 1)`). With the default
`{ type: 'exact' }` precision, results that a JSON number cannot represent
are emitted as exact strings (`"1250/381"` for 1 m in feet) with a note —
never silently rounded.

**Audit, not silent loss.** Every converted value produces an audit entry
keyed by concrete path (`$.sensors[1].altitudeM`) with input, exact value,
output, `lossy` flag, error and error interval.

**Constraints on the complete object, atomic rollback.** Cross-field
constraints run against the fully migrated object. Any failure returns
`{ ok: false, errors }` — no partial output escapes and the input is never
mutated.

**Inverse migrations don't lie.** `invertPlan` (`src/inverse.ts`) inverts
each affine exactly but tags every concrete path whose forward conversion
rounded with `lossless: false` and an explanatory note. Exact round-trips
are marked `lossless: true`.

**Multi-step verification.** `chainExact` converts through a unit chain
with fully deferred rounding and cross-checks against the directly composed
conversion (the exact rational reference). `deferredVsStepwise` quantifies
how much rounding-at-every-step deviates from rounding-once-at-the-end.

**JSON tree semantics.** Input must be a JSON-compatible tree
(`INPUT_NOT_JSON` otherwise). Object key order is irrelevant; array order is
preserved. All errors and audit entries locate their source with concrete
paths.

## Layout

```
src/rational.ts   exact bigint rationals, rounding modes, error intervals
src/units.ts      unit registry: dimensions, affine conversions, cycle checks
src/plan.ts       path patterns, field rules, compilePlan consistency checks
src/migrate.ts    migration engine: audit, constraints, atomic rollback
src/inverse.ts    invertPlan: exact inverse affines, honest losslessness
src/chain.ts      multi-step chains, deferred vs stepwise rounding
src/json.ts       JSON-tree validation with located errors
src/errors.ts     UnitMigrationError with code + path
examples/sample.ts  end-to-end demo
test/*.test.ts    47 tests, node:test
```

## Sample run (abridged)

```
=== absolute vs delta temperature ===
{ "20 C (absolute) -> K": "293.15", "delta 20 C -> delta K": "20",
  "20 C (absolute) -> F": "68", "delta 20 C -> delta F": "36" }

=== migrated object (1 m -> exactly 1000 mm; 20 C -> 293.15 K) ===
sensors[0]: { "tempC": 293.15, "tempDeltaC": 1.5, "altitudeM": 1000 }
sensors[1]: { "tempC": 295.55, "tempDeltaC": 2,   "altitudeM": 500 }

=== audit trail ===
$.sensors[1].altitudeM: input "0.5004", exact "500.4", output 500,
  lossy true, error "-0.4", errorInterval [-0.5, +0.5] (inclusive)

=== inverse audit ===
$.sensors[0].altitudeM: 1000 -> 1,   lossless true
$.sensors[1].altitudeM: 500 -> 0.5,  lossless false,
  note "inverse of a lossy forward migration; this value is approximate"

=== constraint failure -> rolled back ===
[ { code: "CONSTRAINT", path: "$.envelope", message: "..." },
  { code: "CONSTRAINT", path: "$.sensors",  message: "..." } ]
input object untouched by the failed migration: {"minTempC":15,"maxTempC":10}

=== 1 m -> km -> mm, exact at every step ===
[ "1 m", "0.001 km", "1000 mm" ]

=== 8 u1 -> u2 -> u3: deferred vs stepwise ===
{ "exact": "32/9", "deferred": "4", "stepwise": "3", "difference": "1" }
```

## Test results

```
$ npm test
ℹ tests 47
ℹ suites 17
ℹ pass 47
ℹ fail 0
```
