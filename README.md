# exact-decimal-validator

Exact decimal-string validation in TypeScript with **no binary floating point
anywhere**: ranges, total/fraction digit counts and multiples are decided on
scaled `BigInt` coefficients, scientific notation is normalized by definition,
and feasibility is computed by intersecting the range with the multiple and
digit constraints — yielding either a concrete witness or precise no-solution
evidence. No third-party validator (or any dependency) is used.

Requires Node.js >= 22.18 (runs `.ts` directly via type stripping). No install
step, no dependencies.

## Layout

- `src/decimal.ts` — exact decimal core: parse/normalize/compare/multiples.
  Exponents are parsed as `BigInt`; the magnitude budget (`maxScale`, default
  4096) is checked **before** any power-of-ten allocation, so
  `1e999999999999999999` is rejected in microseconds without allocating an
  exponential zero run.
- `src/schema.ts` — the implemented schema subset. Unknown keywords are
  rejected loudly; only what is implemented is promised.
- `src/validate.ts` — `validate` / `contains`; every error carries both
  `instancePath` and `rulePath`.
- `src/intersect.ts` — feasibility intersection: witness or exact evidence.
- `src/counterexample.ts` — minimal destructive counterexample, ordered by
  (totalDigits, |value|, text) and re-verified through the real evaluator.
- `src/reference.ts` — independent naive rational reference used by the
  differential tests ("finite scaled-integer reference").
- `test/` — Node's built-in test runner (`node --test`).
- `examples/demo.ts` — runnable sample (`npm run demo`).

## Schema subset (all bounds are decimal strings, never floats)

```json
{
  "minimum": "0.1",
  "maximum": "9.99",
  "exclusiveMinimum": "-1.5",
  "exclusiveMaximum": "1.5",
  "multipleOf": "0.1",
  "maxTotalDigits": 3,
  "maxFractionDigits": 2
}
```

Semantics worth noting:

- `0.3` **is** an integer multiple of `0.1` (IEEE-754 says
  `0.3 / 0.1 === 2.9999999999999996`; exact arithmetic says `3`).
- Digit counts are numeric, not textual: `1e3` normalizes to `1000` and has
  **4** total digits; `0.30` normalizes to `0.3` and has 1.
- `-0` equals `0` numerically, but the original literal (`text`) is preserved.
- Scientific exponents and negative bounds are handled as exact integers.

## API

```ts
import { validate, contains, intersect, counterexample } from "./src/index.ts";

validate("12.345", { maximum: "10", maxFractionDigits: 2 }, { instancePath: "/items/0/price" });
// => { valid: false, errors: [
//      { instancePath: "/items/0/price", rulePath: "/maximum", ... },
//      { instancePath: "/items/0/price", rulePath: "/maxFractionDigits", ... } ] }

contains({ multipleOf: "0.1" }, "0.3");            // true
intersect({ minimum: "0.15", maximum: "0.25", multipleOf: "0.1" });
// => { feasible: true, witness: "0.2", canonical: "2e-1", ... }
intersect({ minimum: "0.15", maximum: "0.25", multipleOf: "0.5" });
// => { feasible: false, reason: "no-multiple-in-range", summary, probes: [...] }
counterexample({ multipleOf: "0.5" });             // => { found: true, value: "0.25", ... }
```

## How the intersection works

For each fraction-digit scale `f`, values are `a * 10^-f`. The `multipleOf`
constraint `m = M * 10^-sm` becomes an exact integer divisor of `a`, computed
by stripping factors of 2 and 5 from `M`:

```
a * 10^(sm-f) / M ∈ Z   <=>   D_f | a,   D_f = M' * 2^max(0,p-e) * 5^max(0,q-e)
```

with `M = M' * 2^p * 5^q` (M' coprime to 10) and `e = sm - f`. The range
becomes an integer interval at a common scale; `maxTotalDigits` bounds
`|a| <= 10^T - 1` up to trailing powers of ten, which also bounds the scales
that need searching, so the search always terminates. When nothing is found,
the returned `probes` list the exact integer interval and step searched at
each scale — a complete, checkable no-solution certificate.

## Run samples

`npm run demo` (excerpt):

```
=== 0.3 is an integer multiple of 0.1 (binary floats disagree) ===
{ "floatQuotient": 2.9999999999999996, "contains": true }

=== intersect: feasible witness ===
{ "feasible": true, "witness": "0.2", "canonical": "2e-1", "totalDigits": 1, "fractionDigits": 1 }

=== intersect: exact no-solution evidence ===
{
  "feasible": false,
  "reason": "no-multiple-in-range",
  "probes": [
    { "fractionDigits": 0, "integerInterval": ["15", "25"], "step": "100", "candidate": null, ... },
    { "fractionDigits": 1, "integerInterval": ["15", "25"], "step": "50",  "candidate": null, ... }
  ]
}

=== 1e999999999999999999 rejected by budget, no zero-run allocated ===
{ "error": "decimal literal needs scale -999999999999999999, which exceeds the configured budget +/-4096; ...", "elapsedMs": 0 }
```

## Test results

`npm test` (Node's built-in runner, 44 tests, all passing):

```
✔ 0.3 is an integer multiple of 0.1 (no binary float error)
✔ 1e3 digit count is numeric (4), not textual (3)
✔ negative zero equals positive zero, original text preserved
✔ huge exponents are rejected by budget before allocating zeros
✔ precise no-solution evidence when no multiple lies in the range
✔ digit-aware witness: prefers the value within maxTotalDigits
✔ differential: validate agrees with the reference on an exhaustive window
✔ differential: intersect witness is feasible, infeasible means empty window
✔ differential: counterexamples fail according to the reference too
... (44 tests total)
ℹ tests 44
ℹ pass 44
ℹ fail 0
```

The differential suites cross-check the engine against the independent
rational reference in `src/reference.ts`: exhaustive agreement on a
`k * 10^-4` window for randomly generated schemas, witness feasibility,
emptiness of the window exactly when the engine proves infeasibility, and
counterexample validity.
