# stream-composite-unique

Streaming composite-key uniqueness checker for record streams, with a
hand-rolled validator for a small schema subset. Zero dependencies.

Requires **Node.js >= 23.6** (runs TypeScript natively via type stripping; no
build step). Optionally `npm i -D typescript @types/node` if you want `tsc`
type-checking — it is not needed to run the code or the tests.

```bash
npm test    # node --test test/
npm run demo
```

## Features

- **Composite keys over a record stream**: records are consumed one at a time
  from any (async) iterable, so duplicates are detected across arbitrary chunk
  boundaries. Reports the first occurrence and every duplicate position
  (0-based stream indices).
- **Configurable null equality**: `nullsEqual: true` (default) treats null key
  components as equal; `false` makes every null distinct.
- **Memory-bounded**: when the number of distinct keys exceeds
  `maxInMemoryKeys`, key occurrences are spilled to `spillPartitions`
  hash-partitioned JSONL files (equal keys always land in the same partition)
  and a final pass processes one partition at a time. One partition must fit
  in memory — raise `spillPartitions` for skewed key distributions.
- **Collision-free key encoding**: every key component is encoded as
  `<tag><length>:<payload>` and joined with `|`. Because components are
  self-delimiting, separators inside values can never cause collisions, and
  different types never collide (`1` vs `"1"` vs `true` vs `null` vs a missing
  field are all distinct keys). Numbers are canonicalized (`1` ≡ `1.0`,
  `-0` ≡ `0`).
- **Hand-rolled schema validation** (no third-party validator): every error
  carries both `instancePath` (JSON Pointer into the record) and `rulePath`
  (JSON Pointer into the schema).

## Schema subset (the only promised semantics)

Supported keywords — anything else throws `SchemaError` instead of being
silently ignored:

| keyword | applies to | notes |
| --- | --- | --- |
| `type` | all | `"object"`, `"string"`, `"number"`, `"integer"`, `"boolean"`, `"null"` |
| `properties` | object | nested schemas |
| `required` | object | array of property names |
| `additionalProperties` | object | only `false` is meaningful |

`number`/`integer` reject `NaN` and `±Infinity` (not representable in JSON).

## API sketch

```ts
import { CompositeUniqueChecker, fromChunks, validate } from "./src/index.ts";

const report = await new CompositeUniqueChecker({
  fields: ["id", "/user/email"], // names or JSON Pointers
  nullsEqual: true,
  maxInMemoryKeys: 100_000,
  spillPartitions: 256,
  schema,                        // optional, subset above
}).check(recordStream);

// report.duplicates: [{ key, firstIndex, duplicateIndex }, ...] sorted by duplicateIndex
```

With `schema` set, an invalid record throws `RecordValidationError`
(`recordIndex` + `errors[]` with `instancePath`/`rulePath`).

## Layout

- `src/schema.ts` — schema-subset validator
- `src/keyEncoding.ts` — injective composite-key encoding (+ decoder)
- `src/uniqueChecker.ts` — streaming checker with hash-partitioned spill
- `examples/demo.ts` — runnable end-to-end sample
- `test/` — `node:test` suites
