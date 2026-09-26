# stream-array-validator

Dependency-free streaming validator for very large JSON arrays, written in
TypeScript and executed directly by Node.js (>= 22.6, type stripping).
Items arrive one at a time; constraints that a partial stream cannot prove
are only confirmed at `finish()`. No third-party validator is used.

## Guarantees

- **Per-item validation** on every `push()` against the documented schema subset.
- **Length**: `maxItems` fails early on `push()`; `minItems` is only checked
  at `finish()` — a partial stream can never prove a minimum.
- **Uniqueness**: `uniqueItems` (whole item, key-order independent) and
  `uniqueKeys` (named properties) are tracked as canonical hashes in a `Set`,
  so a duplicate of item #0 at position 99 999 is still detected. Items
  themselves are never retained.
- **Aggregates**: `sum` rules with `max` fail early; `min` is only checked
  at `finish()`.
- **Error modes**: `collect` gathers all errors; `fail-fast` throws
  `StreamAbortedError` on the first error and closes the stream.
- **No premature pass**: the overall verdict is `pending` until `finish()`
  returns; only `finish()` may report `valid: true`.
- Every error carries both `instancePath` (JSON Pointer into the data,
  e.g. `/99999/sku`) and `schemaPath` (JSON Pointer into the schema,
  e.g. `/uniqueKeys`).

## Supported schema subset (only this is promised)

- `type`: `object | array | string | number | integer | boolean | null`
- `const`, `enum`
- number/integer: `minimum`, `maximum`
- string: `minLength`, `maxLength`, `pattern`
- object: `properties`, `required`, `additionalProperties` (boolean)
- nested array: `items`, `minItems`, `maxItems`, `uniqueItems`
- top-level stream keywords: `items`, `minItems`, `maxItems`,
  `uniqueItems`, `uniqueKeys`, `aggregates` (`{ kind: "sum", field, min, max }`)

## Usage

```ts
import { ArrayStreamValidator } from "./src/stream.ts";

const v = new ArrayStreamValidator(schema, "collect"); // or "fail-fast"
for (const item of source) v.push(item);   // per-item errors returned/thrown
const result = v.finish();                 // global checks; only valid verdict
```

## Run

```sh
npm test     # node --test (built-in runner), 12 tests incl. 100k-item duplicate
npm run demo # streams 100k items, shows fail-fast and pending-verdict behavior
```
