# alias-resolver

Object alias resolution library (TypeScript). Aliases point either at a
specific immutable object version or at another alias; resolution walks the
chain with a hop budget and returns the full path.

## Model

- `Store` holds immutable object versions (`putObject`) and a versioned alias
  table. Every committed mutation advances a global sequence number; alias
  revisions and object versions are stamped with it, so any past sequence can
  be resolved forever (`snapshot` option).
- `AliasTarget` is `{ kind: 'object', objectId, version }` or
  `{ kind: 'alias', alias }`. Multiple aliases may share one target.

## Resolution

`resolveUncached(store, name, { maxHops, snapshot })` is the cache-free
reference traversal. Results:

- `{ ok: true, path, object }` — full alias path plus the terminal object ref.
- `{ ok: false, reason: 'cycle', path, cycle }` — loop reported with its members.
- `{ ok: false, reason: 'dangling', path, missing }` — next hop (alias or
  object version) does not exist.
- `{ ok: false, reason: 'max-hops', path }` — hop budget exhausted.

## Atomic batches

`store.applyBatch(ops, { allowDangling })` validates the whole batch against
an overlay of committed state and applies it all-or-nothing (one sequence
tick per committed batch). Only aliases whose resolution could change — the
touched aliases plus transitive reverse dependents — are re-resolved.
Rejection reports `cycles` and `dangling` as separate lists. Cycles are
always rejected; dangling chains are rejected unless `allowDangling: true`
(they are still reported either way). Aliases deleted by the batch itself are
exempt from the dangling check; aliases left pointing at them are not.

## Cache with negative dependencies

`CachedResolver` memoizes latest-state resolution. Each entry records every
read the traversal performed, including **negative dependencies** (aliases
and object versions that were absent). A committed change invalidates exactly
the entries whose dependency keys intersect the change set, so filling in a
previously missing target flushes the stale "dangling" results. Historical
snapshots are immutable and should be read via `resolveUncached`.

## Verification

`npm test` compiles and runs `test/run-tests.ts`: targeted cases (shared
targets, batch-created cycles, separate cycle/dangling reports, negative-dep
invalidation, delete-then-recreate, old snapshots) plus a seeded differential
fuzz that cross-checks the cached resolver against the uncached traversal
after every randomized operation and replays captured snapshots at the end.
