# Streaming Large-Array Validator (TypeScript, zero dependencies)

A hand-written, streaming validator for **very large arrays**. Items are fed in
one at a time; the validator incrementally maintains:

- **length** — accepted item count (`maxItems` checked on every push, `minItems`
  only at `finish()`),
- **unique keys** — one `Set` per `uniqueKeys` entry (O(1) duplicate check,
  supports dotted nested paths like `profile.email`),
- **aggregate constraints** — running sums checked at `finish()`.

It implements **only an explicit subset** of JSON Schema and uses no third-party
validation library.

## Design rules enforced

- **`minItems` / aggregate bounds can only be confirmed at `finish()`** — future
  items can still change the result.
- **An unfinished stream can never report overall pass**: `status().valid` is
  always `false` while `state === "open"`, and no `ValidationResult` exists
  before `finish()`.
- **Every error carries both paths**:
  - `instancePath` — RFC 6901 JSON Pointer into the data (root is `""`),
  - `rulePath` — JSON Pointer into the schema/rule that failed
    (e.g. `items/properties/email/pattern`, `uniqueKeys/id`,
    `aggregates/total/sum/minimum`).
- **Two error policies** (`onError`):
  - `"collect"` (default) — invalid items are rejected (not counted, not
    observed), their issues are returned and accumulated, processing continues;
  - `"throw"` — the first invalid item throws `ValidationError` and stops early.
- Failed items never mutate length / uniqueness tables / sums, so later
  duplicates stay deterministic.

## Supported schema subset

`type` (`string | number | integer | boolean | object | array`), `items`
(arrays; recursively nested), `properties`, `required`, `minLength`,
`maxLength`, `pattern`, `minimum`, `maximum`, `enum`, plus array-level
`minItems`, `maxItems`, `uniqueKeys: string[]` (dotted paths) and
`aggregates: [{ key, sum: { minimum?, maximum? } }]`. Unknown keywords are
ignored (subset promise — nothing else is claimed).

## Usage

```ts
import { StreamArrayValidator } from "./src/index.ts";
import type { Schema } from "./src/index.ts";

const schema: Schema = {
  minItems: 2,
  maxItems: 100_000,
  uniqueKeys: ["id", "customer.email"],
  aggregates: [{ key: "total", sum: { minimum: 10, maximum: 1000 } }],
  items: {
    type: "object",
    required: ["id", "total", "customer"],
    properties: {
      id: { type: "integer", minimum: 1 },
      total: { type: "number", minimum: 0 },
      customer: {
        type: "object",
        required: ["email"],
        properties: { email: { type: "string", pattern: "@" } },
      },
    },
  },
};

const validator = new StreamArrayValidator(schema); // or (schema, "throw")
for await (const item of someAsyncIterable) {
  const issues = validator.push(item); // issues for this item (collect mode)
}
const result = validator.finish();
// result: { valid, state: "ended", length, issues[], sums }
```

Requires **Node.js >= 22** (uses the built-in `node:test` runner and native
TypeScript type stripping; no packages to install).

## Run

```bash
node --experimental-strip-types --test test/*.test.ts   # tests
node --experimental-strip-types examples/demo.ts         # runnable sample
# or: npm test / npm run demo
```

## Files

- `src/types.ts` — public types
- `src/validator.ts` — `StreamArrayValidator`, `ValidationError`, `validateArray`
- `src/index.ts` — exports
- `examples/demo.ts` — 7 runnable scenarios incl. the 100,000-item duplicate case
- `test/validator.test.ts` — 23 tests with `node:test`/`node:assert`
- `test-results.txt`, `demo-output.txt` — captured outputs
