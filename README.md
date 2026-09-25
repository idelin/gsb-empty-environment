# stream-transform-records

Streaming record transforms over JSON-compatible trees, with two failure
semantics, path-located errors, and crash-safe checkpoints. Zero
dependencies; TypeScript executed via Node's built-in type stripping
(Node >= 23.6) and tested with the built-in `node:test` runner.

## Guarantees

- **Two modes**
  - `isolated`: each record stands alone — one bad record is rejected and
    logged, later good records still flow through.
  - `atomic`: all-or-nothing — any failure publishes nothing; success
    publishes the whole batch at once.
- **Located errors**: every `TransformError` carries `recordId`, `step`,
  and a JSON `path` (e.g. `$.items[2].name`) pointing at the offending
  value inside the tree.
- **Ordered output**: records are published in input order, in both modes.
- **No duplicate output**: the checkpoint records every emitted/failed/
  staged record id; a restarted run skips them instead of re-emitting.
- **No double counting after recovery**: counts are derived from the
  durable checkpoint log, so a failed record is counted exactly once no
  matter how many times the batch is retried.
- **JSON tree semantics**: object key order is insignificant, array order
  is significant (`canonicalize`/`jsonEquals` in `src/json.ts`).

## Layout

- `src/json.ts` — JSON tree types, path format/parse/resolve, canonicalization
- `src/errors.ts` — `TransformError` (recordId + step + path), `failAt`
- `src/pipeline.ts` — named steps and the per-record pipeline runner
- `src/checkpoint.ts` — in-memory and append-only JSONL file checkpoint stores
- `src/runner.ts` — `transformStream` with `atomic` / `isolated` modes
- `test/` — `node:test` suites
- `examples/sample.ts` — runnable end-to-end demo

## Usage

```ts
import { defineStep, failAt, transformStream, FileCheckpointStore } from './src/index.ts';

const validate = defineStep('validate', (rec) => {
  if (typeof rec.name !== 'string') failAt(['name'], 'must be a string');
  return rec;
});

const result = await transformStream({
  records,                       // iterable or async iterable of JSON objects
  steps: [validate /*, ...*/],
  mode: 'isolated',              // or 'atomic'
  checkpoint: new FileCheckpointStore('checkpoint.jsonl'),
  onEmit: (rec, id) => console.log('published', id),
});
// result.emitted — published records, input order
// result.errors  — TransformError[] with recordId / step / path
// result.stats   — { seen, emitted, failed, skipped }
```

## Run

```sh
npm test          # node --test 'test/*.test.ts'
npm run sample    # node examples/sample.ts
```

## Sample output (`npm run sample`)

```
=== 1. isolated mode: one bad record never blocks the good ones ===
  -> emitted {"id":"r1","name":"APPLES","total":3}
  -> emitted {"id":"r3","name":"CHERRIES","total":5}
  -> emitted {"id":"r5","name":"DATES","total":4}
  xx rejected {"recordId":"r2","step":"validate","path":"$.items[1].qty","message":"record \"r2\" failed at step \"validate\" path $.items[1].qty: must be a number"}
  xx rejected {"recordId":"r4","step":"validate","path":"$.name","message":"record \"r4\" failed at step \"validate\" path $.name: must be a string"}
  stats: {"seen":5,"emitted":3,"failed":2,"skipped":0}

=== 2. atomic mode: any failure publishes nothing ===
  committed: false, emitted: 0
  first error: record "r2" failed at step "validate" path $.items[1].qty: must be a number

=== 3. crash mid-stream, then recover from the file checkpoint ===
  !! crashed: simulated crash (process killed); emitted so far: [r1,r3]
  resumed run stats: {"seen":5,"emitted":1,"failed":1,"skipped":3}
  total emitted across both runs (no duplicates): [r1,r3,r5]
  durable counts: {"emitted":3,"failed":2,"staged":0}
```

## Test results (`npm test`, Node v23.9.0)

```
▶ json paths
  ✔ formats nested object/array paths
  ✔ round-trips through parsePath
  ✔ locates values in a tree
▶ canonical json semantics
  ✔ treats object key order as insignificant
  ✔ treats array order as significant
▶ isolated mode
  ✔ emits good records in input order and keeps going after bad ones
  ✔ locates every error by record id, step, and json path
  ✔ does not re-emit or re-count records on rerun with the same checkpoint
▶ atomic mode
  ✔ publishes nothing when any record fails
  ✔ publishes the whole batch in order when everything passes
▶ crash recovery with a file checkpoint
  ✔ isolated: resumes without duplicate output and without double-counting failures
  ✔ atomic: resumes a crashed batch and publishes it exactly once
ℹ tests 12
ℹ pass 12
ℹ fail 0
```
