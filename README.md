# reactive-core

A dependency-tracking reactive engine in TypeScript with **zero dependencies**.
Runs directly on Node.js ≥ 23.6 (native type stripping); tests use the Node.js
built-in test runner. No reactive framework is used — everything is implemented
in `src/reactive.ts`.

## Semantics

- **Actual-read dependency tracking.** A computed subscribes to exactly the
  nodes its callback read during the last run. When a branch changes
  (`if (flag, a, b)`), the dead branch is unsubscribed, so `a` no longer
  triggers recomputation; newly read branches are subscribed.
- **Atomic batches.** Writes inside `world.batch(...)` commit together.
  Effects and external readers only observe the committed snapshot — never
  intermediate states. Repeated writes in one transaction resolve to a single
  fixed result on one consistent snapshot (last write wins, one notification).
- **Next-round isolation for re-entrant writes.** A `set` issued from inside a
  compute callback (or effect) is deferred and applied as a separate
  transaction round after the current one commits. A computation never
  observes its own side effects, and no intermediate state is ever notified.
  A system that never reaches a fixed point fails with `UnstableError`
  instead of hanging.
- **Errors as values.** A throwing compute callback stores the error; readers
  rethrow it, downstream computeds propagate it, and the node recovers
  automatically once its inputs change.
- **Cycle detection.** Reaching a node that is already being refreshed throws
  `CycleError` carrying the concrete node chain (e.g. `A -> B -> A`). The
  error is stored like any other result, so dynamically formed cycles report
  the chain and recover when a later write breaks the cycle.
- **Minimal recomputation.** Version counters + value-equality (`Object.is`)
  ensure a node recomputes at most once per round and only when a dependency
  actually changed; unchanged intermediate values stop propagation (diamonds
  recompute each node exactly once).
- **Replayable operation log.** Every applied write is recorded
  (`world.log`, with `commit` markers per transaction). `world.applyLog(log)`
  reproduces the exact same state on a freshly built world.

## Verification

`tests/model.test.ts` checks the incremental engine against an **independent
oracle** that re-evaluates every computed from scratch after every event:

- exhaustive exploration of all event sequences up to length 3, plus seeded
  random sequences of length 12 with multi-write batches, over three graphs
  (diamond + dynamic branch, dynamic cycle forming/breaking, error
  propagation);
- after every event: values/errors match the oracle, reported cycle chains
  are real cycles in the oracle's read graph, no computed recomputes more
  than once per event, the probe effect only observes oracle-consistent
  snapshots, and the operation log replays to the oracle's state;
- actual recomputed nodes are counted and reported.

## Run

```sh
npm test    # node --test 'tests/*.test.ts'
npm run demo
```

## Test results

```
  [exhaustive] events=1384 engineRecomputes=3311 oracleEvals=5832 distinctRecomputedNodes=12
  [random] events=5400 engineRecomputes=12894 oracleEvals=21600 distinctRecomputedNodes=12
✔ model: exhaustive bounded event exploration (all sequences up to length 3)
✔ model: seeded random exploration with batches (length 12, 150 sequences per spec)
✔ dynamic branch: if(flag,a,b) — switching flag unsubscribes the dead branch
✔ diamond: each node recomputes exactly once per update
✔ necessity: unchanged intermediate value stops propagation
✔ batch: subscribers observe only the committed snapshot
✔ deferred write from a compute callback applies in the next round
✔ compute callback errors propagate to readers and recover
✔ self-write loop is reported as unstable instead of hanging
✔ static cycle returns the concrete node chain
✔ dynamic cycle: forms, reports the chain, then breaks and recovers
✔ operation log replays to identical state
ℹ tests 12   ℹ pass 12   ℹ fail 0
```

## Demo output (`npm run demo`)

```
=== diamond graph ===
d = 4
after s=2, d = 7
recomputes: { b: 2, c: 2, d: 2 }

=== dynamic branch if(flag, a, b) ===
sel = 1 deps = [ 'flag', 'a' ]
sel = 10 deps = [ 'flag', 'b' ]
a.set(999): sel = 10, recomputes 2 -> 2 (dead branch ignored)

=== atomic batch ===
effect observed: [ 0, 5 ] (single notification, final values only)

=== cycle detection and recovery ===
A = 1 B = 2
cycle: dependency cycle detected: A -> B -> A
recovered: A = 41 B = 82

=== deferred write (next-round isolation) ===
c = 5 -> mirror settled at 5 in the following round

=== operation log replay ===
log: [{"type":"set","name":"s","value":2},{"type":"commit"},{"type":"set","name":"s","value":3},{"type":"commit"}]
replayed double = 6 (live: 6)
```
