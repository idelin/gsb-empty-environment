# entity-store

A dependency-free normalized entity store in TypeScript. Nested server
responses are split into per-type entity tables; parents hold shared
`{ $ref: "Type:id" }` references, so every reader aliases the same record.
No reactive framework — live queries, optimistic overlays and a replayable
operation journal are built by hand.

Requires Node.js >= 22.6 (runs TypeScript directly via type stripping;
developed against v23.9.0, see `.nvmrc`). No dependencies, no build step.

```sh
nvm use            # or any node >= 22.6
npm test           # node --test test/*.test.ts
npm run demo       # node examples/demo.ts
```

## Model

- **Entity tables** — `ingest(type, payload)` normalizes a nested response:
  any object with an id + `__typename` becomes an entity keyed `Type:id`;
  everything else stays embedded in its parent.
- **Shared references** — two parents referencing the same entity both see
  updates synchronously, because they hold the same `$ref`.
- **Missing vs null** — absent fields keep their previous value (partial
  responses / partial updates); explicit `null` is stored as `null`;
  `undefined` means "not sent".
- **Id-less nested objects** are rebuilt wholesale on every write and never
  merge by identity, so two parents' inline objects can't be confused.
  Entities nested *inside* an inline object still alias the shared table.
- **Reverse index** — `target -> (source -> count)` is maintained
  incrementally on every write. `delete(key)` tombstones the entity, removes
  it from the index and rewrites every referrer (ref -> `null`, array entries
  dropped), cycle-safe. Tombstones block stale writes from resurrecting the
  entity; a newer version may.
- **Paginated relations** — `appendConnection(key, field, items, pageInfo)`
  appends a page, dedupes by ref, merges `pageInfo`, and leaves sibling
  fields alone. Item payloads are normalized as usual.

## Versions, overlays, rollback

- Every committed op carries an explicit version (Lamport-style clock, or a
  caller-supplied one). A field write is only applied if its version is >=
  the stored field version — **an old server response can never overwrite a
  newer user-submitted field version**.
- `beginOptimistic()` opens an overlay layer stacked above committed state;
  readers see the overlay immediately. `commitLayer(layerId, confirm?)`
  accepts the layer's writes into the base at the layer's version and
  optionally applies the server's confirmation on top — atomically, in one
  commit. `rollbackLayer(layerId)` discards exactly that layer's own writes;
  concurrent layers and base state are untouched.
- `mergeIdentities(from, to)` merges two keys (e.g. an optimistic temp id and
  the server-assigned id): fields merge per-version, every referrer and all
  pending layer writes are retargeted atomically, and the old key becomes an
  alias.

## Isolation & replay

- `store.tx(t => ...)` buffers ops and commits them as one atomic state
  transition; a throwing callback commits nothing. Public reads and snapshots
  always observe committed state — never a transaction's intermediate state —
  while `tx.get` gives the writer read-your-writes. Every commit is
  copy-on-write, so a `snapshot()` stays frozen forever.
- `store.query(read => ...)` evaluates against the current state and records
  the read keys as a **lease**; after each commit, queries whose lease
  intersects the changed keys re-evaluate and notify subscribers only when
  the result actually changed. `dispose()` releases the lease.
- Every mutation is journaled as a versioned op. `Store.replay(journal)`
  rebuilds the exact same state — verified by deep equality in the tests.

## Layout

- `src/types.ts` — `Value`/`Ref` model, deep equality, ref collection
- `src/normalize.ts` — nested response -> per-entity patches
- `src/state.ts` — immutable state, op application, reverse index, overlays
- `src/store.ts` — `Store`, transactions, snapshots, live queries
- `test/` — node:test suites; `examples/demo.ts` — end-to-end walkthrough
