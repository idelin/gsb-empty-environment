# or-counter-crdt

Observed-reset multi-replica counter (state-based CRDT) in TypeScript.

## Semantics

- **Operations, not values.** Every mutation is an operation with a globally
  unique id: `replicaId#logicalSeq`. Ids come from a per-replica logical
  sequence — never wall-clock time — so merge correctness does not depend on
  clock synchronization or "last write wins".
- **Increment** (`{kind:"inc", id, amount}`) contributes `amount`.
- **Reset** (`{kind:"reset", id, observed}`) names the exact set of operation
  ids it had observed when issued and clears exactly those contributions.
  Increments the reset did not observe — concurrent ones, or ones still in
  flight — survive. Concurrent resets compose by union of their observed sets.
- **Merge** is set union over operations, deduplicated by unique op id
  (`Map` keyed by id; re-applying an existing id is a no-op). It is
  commutative, associative and idempotent, so message reordering, duplication
  and repeated merges cannot affect the converged state.
- **Value** = sum of amounts of all increment ops not named by any known
  reset's `observed` set.

## API

```ts
import { ObservedResetCounter } from "./src/counter.js";

const a = new ObservedResetCounter("replica-a");
const b = new ObservedResetCounter("replica-b");

const inc = a.increment(3);      // op message to broadcast
b.apply(inc);                    // idempotent: duplicates are ignored

const reset = b.reset();         // captures ids of ops b has observed
a.apply(reset);                  // clears only what b had observed

b.merge(a.snapshot());           // state-based merge (union by op id)
a.value();                       // current counter value
```

## Tests

`test/counter.test.ts` verifies the op-set semantics:

- message **permutation invariance** (all 7! delivery orders of a 7-op
  scenario converge to the same state and value),
- **duplicate** delivery and repeated merge idempotence,
- **reset overlap**: concurrent resets clear the union of observed sets,
  unobserved concurrent increments survive,
- merge commutativity/associativity, JSON round-trip, and a seeded
  randomized gossip convergence check against a reference model.

```sh
npm install        # requires network (typescript, @types/node)
npm test           # Node 18+: tsc build + node --test
```

Offline / Node 16 fallback (no dependencies; uses the compiled `.build/` output —
regenerate it first with `npm run build` if it is missing):

```sh
node --experimental-loader=./scripts/node16-loader.mjs scripts/run-tests-node16.mjs
```
