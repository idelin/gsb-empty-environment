# Shard Handoff Simulator (分片交接模拟器)

Simulates migrating a shard from A (甲) to B (乙) with a switchover fence,
and verifies — step by step — that every acknowledged write eventually
becomes visible on B and that deleted records never resurrect.

## Design

- **Versioned mutations** (`src/shard.ts`): every write accepted by a shard
  gets a monotonically increasing version. Deletes are stored as
  *tombstones* carrying a version, so a stale replicated put can never
  bring a deleted record back.
- **Base copy while writes continue** (`src/coordinator.ts`): the
  coordinator streams a deliberately non-atomic base copy (chunked reads of
  A's store, tombstones included) while A keeps accepting writes.
- **Out-of-order, re-sent deltas** (`src/link.ts`): A's mutation log is
  shipped over an unreliable link that reorders and duplicates messages.
  B applies them idempotently: an incoming mutation wins only if its
  version is strictly newer than the stored one. A contiguous high-water
  mark tracks drain progress (stale redeliveries still count, since their
  effect is already reflected).
- **Switchover fence**: once the base copy is sent and the log is caught
  up, the coordinator fences A. A then **rejects new writes** but still
  **returns cached results for old requests** (idempotent retries). When
  B's high-water mark reaches A's final sequence, B is activated with a
  version counter continuing past A's final sequence.
- **Step-by-step checker** (`src/checker.ts`): after every simulation event
  it verifies (1) B's per-key version never regresses (no resurrection),
  (2) every record on B exactly matches the accepted mutation for its
  version, and (3) at the end, every acknowledged write is visible on B.

## Run

```sh
npm run build   # requires typescript (tsc)
npm start       # or: npm test
```

Three deterministic scenarios run and print a trace:

1. Baseline migration with reordered + duplicated deltas.
2. Delete-resurrection stress (hot keys, heavy delete traffic).
3. Fence edge: writes in flight exactly at the fence, post-fence retries
   served from A's cache, rejected writes resubmitted to B.

Exit code is 0 only if all invariants hold in all scenarios.
