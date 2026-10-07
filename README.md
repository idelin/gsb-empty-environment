# optimistic-store

A TypeScript state library that replays pending operations on top of a
server-confirmed baseline, designed for optimistic UI over an unreliable,
out-of-order network.

## Model

- **baseline** — server-confirmed state. Advances only by committing
  acknowledged operations, strictly in submission order (head-of-line drain).
- **pending queue** — locally submitted ops without a final server receipt.
  The visible state is `baseline` + every currently-applicable pending op
  replayed in submission order.

## Receipt semantics (out-of-order safe)

- **accept** — buffered until all earlier ops are resolved, then committed
  into the baseline in submission order.
- **reject** — takes effect immediately, wherever the op sits in the queue.
  Exactly that op is removed and its successors are replayed. The store
  never restores a saved snapshot, so a rejection can never wipe out other
  successful operations (committed baseline entries or unrelated optimistic
  ops).
- **dependency-failed** — after any change, the queue is replayed over the
  baseline; an op whose target no longer exists (or already exists, for
  `create`) is flagged `dependency-failed` and excluded from the visible
  state. The flag is *derived and recoverable*: if the op that destroyed
  the object is itself rejected later, the dependent op becomes applicable
  again. An op only leaves the queue when its own receipt arrives, so a
  late server accept for a previously-failed op is still honored and
  commits in the correct position.

## Verification

- `test/store.test.ts` — deterministic scenarios: out-of-order accepts,
  rejection replay, dependency-failure marking and recovery, duplicate /
  unknown / contradictory receipts, no snapshot clobbering.
- `test/property.test.ts` — model-based property test. A serial reference
  model (`test/reference.ts`) plays the server: ops apply strictly in
  submission order, with random rejections. Receipts are delivered to the
  store in random order, both after full submission and interleaved with
  submissions. After **every** event the visible state must equal the
  reference model; after the last receipt the baseline must equal the
  serial model's final state. Run with `PROPERTY_TRIALS=<n>` to scale.

## Requirements & usage

Requires Node.js ≥ 22.6 (native TypeScript type stripping) — no build
step and no dependencies.

```sh
npm test                          # run the test suite
PROPERTY_TRIALS=3000 npm test     # heavier property-test sweep
```

```ts
import { OptimisticStore } from './src/store.ts';

const store = new OptimisticStore<Record<string, unknown>>();
store.submit({ id: 'a', kind: 'create', target: 'x', value: { n: 1 } });
store.submit({ id: 'b', kind: 'update', target: 'x', value: { n: 2 } });
store.state();        // Map { 'x' => { n: 2 } }  (optimistic)
store.receiveReceipt('b', true);   // buffered: 'a' is still ahead
store.receiveReceipt('a', false);  // 'a' removed; 'b' now fails...
store.statusOf('b');  // 'dependency-failed' (x was never created)
```
