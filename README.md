# reactive-core

A minimal dependency-tracking reactive core in TypeScript with **no reactive
framework**. Runs on Node.js (>= 22.6) via native type stripping — no build
step, no dependencies.

```bash
npm test       # node --test (unit tests + finite event exploration)
npm run demo   # runnable tour: diamond / branch / tx / cycle / re-entrancy
```

## Model

- `state(name, initial)` — mutable cell. `set` outside a transaction is an
  implicit one-write transaction.
- `computed(name, fnId, fn)` — derived node. `fn(cx)` reads other nodes via
  `cx.get(name)`. Recomputes lazily, only when a dependency it *actually read
  last time* changed.
- `effect(name, fnId, fn)` — subscriber. Runs once per commit round in which
  any of its dependencies changed; runs immediately on creation.
- `tx(fn)` — transaction. All writes inside are staged and applied atomically
  at commit.

## Semantics

**Dynamic dependencies.** The read set is tracked while a callback executes
and replaces the previous set after every run (success or failure). In
`c = flag ? a : b`, flipping `flag` unsubscribes the dead branch: writing `a`
afterwards never re-triggers `c`.

**Glitch-free batching.** A commit applies all staged writes on one
consistent snapshot, marks the transitive dependents dirty, then runs dirty
effects in creation order; effects pull computed nodes, which recompute in
dependency (topological) order. A diamond join recomputes exactly once.
Subscribers never observe intermediate states.

**Transactions.** Writes inside `tx` are staged. Repeated writes to the same
state collapse to the last one. Inside a transaction, direct state reads see
staged values (read-your-writes), but computed nodes always expose the last
committed snapshot — a fixed result on one consistent snapshot, never
recomputed per intermediate write. An exception in the callback rolls back
the staged writes and the logged operations.

**Re-entrant writes.** A `cx.set` issued from inside a computation is
buffered per computation frame and applied in the *next* commit round, after
the current round has fully settled. Writes from a computation that throws
are discarded. Notifications therefore never observe a half-applied write.

**Errors.** A throwing computed stores the error (readers rethrow it) and
adopts the partial dependency set it read before throwing, so the failure is
retried exactly when one of those reads changes. Effect failures are
collected in `commitErrors`; other effects still run.

**Cycle detection.** Cycles are detected against the live evaluation stack
and reported as `CycleError` with the concrete node chain (e.g.
`ca -> cb -> ca`). Because dependencies are dynamic, cycles form and break
with data; when the breaking write arrives, invalidation flows through the
partial dependency sets and the nodes recover.

**Replay.** Every mutating operation is appended to `system.log`
(`state` / `computed` / `effect` / `set` / `tx`). `replaySystem(registry, log)`
rebuilds an identical system — snapshots and effect notification histories
match exactly. Function bodies are not serializable, so the `FnRegistry`
(mapping `fnId` -> function) is shared between the original and the replay.

## Verification

`test/core.test.ts` — unit tests: lazy caching, exact diamond recompute
counts, branch dependency drop/acquire, exact cycle chains, dynamic cycle
form/break/recovery, transaction atomicity, error recovery, re-entrant write
isolation, log replay.

`test/exploration.test.ts` — finite event exploration. A seeded generator
emits random writes, multi-write transactions (with repeated writes to the
same state), and flag toggles that dynamically form and break cycles, over a
graph containing a diamond, a dynamic branch, a conditional cycle pair, and a
re-entrant writer. After **every** event the incremental engine is compared
against `src/model.ts`, an independent model that recomputes *everything*
from scratch after each commit round: all computed values/errors (including
canonicalized cycle chains), all effect outputs, and all effect error states
must match. The test also counts actual recomputations on both sides.

Latest run: 24 seeds x 150 events (1046 transactions), 909 snapshots
containing a detected cycle, engine recomputations 23091 vs model 46876 —
less than half, with identical observable state at every step.

## Demo output (`npm run demo`)

```
[1] diamond: s -> left,right -> join
  join = 5
  recompute counts after initial run: { left: 1, right: 1, join: 1, view: 1 }
  after s=10, join = 23
  recompute counts after one write: { left: 2, right: 2, join: 2, view: 2 }

[2] dynamic branch: c = flag ? a : b
  c = 1
  flag=false -> c = 100
  a=999 (dead branch): c recomputed 0 times, c = 100
  b=200 (live branch): c = 200

[3] transaction: repeated writes, single consistent notification
  notifications after tx: [ { x: 0, y: 0 }, { x: 2, y: 3 } ]

[4] cycle detection with concrete node chain
  flags off: [ 7, 9 ]
  flags on : dependency cycle detected: ca -> cb -> ca
  break fa : [ 7, 7 ] (recovered)

[5] re-entrant write from a computed (next-round isolation)
  notifications: [ {s:0,t:0,c:0}, {s:5,t:0,c:5}, {s:5,t:5,c:5} ]
```
