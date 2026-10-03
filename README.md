# ARIES Crash Recovery + Incremental Snapshots (TypeScript)

Classic ARIES write-ahead logging recovery protocol with lightweight
incremental snapshots and checkpoint archiving. Pure TypeScript on Node.js
(>= 22.6, type-stripping), zero dependencies, tested with `node:test`.

## Architecture

- `src/log-record.ts` — WAL record types: `begin/update/commit/abort/end/clr/checkpoint`.
- `src/wal.ts` — `LogManager`: buffered append, `flush()` for durability,
  `discardBuffer()` simulates crash loss of the unflushed tail, `truncateBefore()`
  prunes the durable prefix after archiving.
- `src/storage.ts` — `DiskStorage` (atomic JSON page file) + `BufferPool`
  (steal/no-force, pageLSN tracking, copy-on-write hooks for snapshots).
- `src/engine.ts` — `Database`: transactions, fuzzy checkpoints, and restart
  recovery with the three ARIES phases:
  - **Analysis** — rebuilds the TransactionTable and DirtyPageTable starting
    from the most recent checkpoint.
  - **Redo** — repeats history from `min(recLSN)`, skipping records already
    reflected in `pageLSN`.
  - **Undo** — max-LSN-first reverse scan over loser transactions, writing
    CLRs (with `undoNextLSN`) so restarted recovery never undoes twice.
- `src/snapshot.ts` — `IncrementalSnapshotManager`: each snapshot persists
  only pages with `pageLSN` in `(baseLSN, horizonLSN]` (delta chain, first
  snapshot is the full base). Copy-on-write pins the snapshot to its horizon
  LSN while writes continue; batched `setImmediate` yields keep pauses low.
  `archiveCheckpoint()` copies the WAL prefix into `archive/`, snapshots, and
  prunes the live WAL at a safe horizon (oldest active transaction's begin
  LSN, so loser undo chains survive).

## Run

```sh
npm test     # node --test tests/*.test.ts  (12 tests)
npm run demo # end-to-end crash/recovery walkthrough
```

## Acceptance scenario (tests/aries.test.ts)

Builds a WAL with 2 committed (T1, T2) and 3 uncommitted (T3, T4, T5)
transactions, a fuzzy checkpoint in the middle, a stolen uncommitted page,
and an unflushed tail record — then crashes. Recovery restores exactly the
committed state, emits one CLR per loser update, and leaves no leaks
(empty transaction table, flushed log buffer, clean buffer pool).
