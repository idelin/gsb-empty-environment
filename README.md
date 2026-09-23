# tx-batch-store

Transactional batched state store in TypeScript — no reactive framework, no
dependencies. Runs directly on Node.js ≥ 22.6 via native type stripping
(developed on Node 23.9).

## Semantics

- **One commit per scheduling cycle** — every operation enqueued inside
  `store.batch(...)` is evaluated **in order** against a private transaction
  draft, then committed exactly once with a single subscriber notification.
- **Ordered mixed updates** — direct assignments (`set`) and functional
  updates (`update`) keep their relative order; a functional update reads the
  value left by the previous operation, so two consecutive `x => x + 1` add 2.
- **Conditionals, invariants, read-then-write** — `setIf` / `updateIf`
  predicates, cross-field `assert` invariants, and updaters all receive the
  in-flight draft and are evaluated in queue order before the single commit.
- **Cancellation** — each enqueued op returns a handle; `handle.cancel()`
  drops only that op. Later independent updates still apply.
- **Idempotent retry** — ops may carry `{ opId }`; an already-applied `opId`
  is skipped, so retries never double-apply.
- **Atomicity** — any throw (including a violated invariant) rolls the whole
  batch back: no partial values, no notification, no log entries.
- **Nested batches** — inner `batch` calls join the outer transaction; only
  the outermost batch commits and notifies.
- **Reader isolation** — `getState()` / `tx.read()` / subscribers only ever
  see committed state, never the transaction draft.
- **Replay** — every applied op is logged as a resolved `{ commit, key, value }`
  entry; `Store.replay(initial, store.log)` rebuilds the exact same state.

## API sketch

```ts
const store = new Store({ balance: 100, escrow: 0, total: 100 });
store.subscribe((state, prev) => { /* called once per commit */ });

store.batch((tx) => {
  tx.update('escrow', (e, d) => e + Math.min(40, d.balance)); // read-then-write
  tx.update('balance', (b) => b - 40);
  tx.updateIf('escrow', (d) => d.balance < 50, (e) => e + 5);  // conditional
  tx.assert((d) => d.balance + d.escrow === d.total, 'funds conserved');
});
```

## Run

```sh
npm test    # node --test (Node built-in test runner)
npm run demo
```

## Demo output (`node examples/demo.ts`)

```
== 1. one scheduling cycle: mixed ops merged into a single commit ==
  [notify #1] {"phase":"ready","balance":62,"escrow":40,"total":102,"ops":1}
  state: {"phase":"ready","balance":62,"escrow":40,"total":102,"ops":1} | notifications: 1

== 2. nested batches: only the outermost notifies ==
  [notify #2] {"phase":"ready","balance":62,"escrow":40,"total":102,"ops":3}
  ops: 3 | notifications in this step: 1

== 3. cancel one op; later independent updates survive ==
  [notify #3] {"phase":"ready","balance":62,"escrow":40,"total":102,"ops":13}
  balance: 62 (cancelled, unchanged) | ops: 13

== 4. idempotent retry by operation ID ==
  [notify #4] {"phase":"ready","balance":57,"escrow":40,"total":102,"ops":13}
  balance after 3 identical retries: 57 (debited once)

== 5. failed batch: invariant violated, no partial values ==
  rolled back: Invariant violated: no overdraft
  state still: {"phase":"ready","balance":57,"escrow":40,"total":102,"ops":13}

== 6. replay the operation log from the initial snapshot ==
  replayed: {"phase":"ready","balance":57,"escrow":40,"total":102,"ops":13}
  matches live state: true
  log entries: 12 | commits: 4 | notifications: 4
```

## Test results (`node --test`)

```
✔ multiple updates in one cycle merge into a single commit and notification
✔ functional updates read the previous queued value; direct/functional order preserved
✔ two consecutive x => x + 1 add 2, not 1
✔ conditional updates and read-then-write evaluate in order on the draft, committed once
✔ conditionals see earlier ops of the same batch, not the committed state
✔ cancelling one operation does not undo later independent updates
✔ operation IDs make retries idempotent
✔ merged optimization and unmerged execution agree on result and side-effect count
✔ merged and unmerged raise the same error with the same side-effect count
✔ readers never observe transaction intermediate state
✔ operation log replays to the exact same state
✔ nested batches: only the outermost batch commits and notifies
✔ failed batch leaves no partial values and does not notify
✔ a throwing updater aborts the batch; the store stays usable afterwards
ℹ tests 14  ℹ pass 14  ℹ fail 0
```
