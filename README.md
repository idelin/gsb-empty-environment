# page-txn-log

ARIES-style local page transaction log in TypeScript (zero dependencies, Node >= 22.6
type-stripping; developed and verified on Node v23.9.0).

## Run

```sh
# use a Node that supports type stripping (>= 22.6 with flag, >= 23.6 by default)
node --test test/*.test.ts     # 18 tests
node src/cli.ts all            # end-to-end scenarios against real files under ./run/
node src/cli.ts demo|crash-rollback|torn-checkpoint|truncate
```

## Design

- **Log records** (`src/types.ts`): `update` carries before/after images and `prevLSN`;
  `clr` additionally carries `undoNext`; plus `commit` / `abort` / `end` and
  `chkpt_begin` / `chkpt_end` (end embeds the txn table and dirty page table).
- **File layer** (`src/filelayer.ts`): append-only segment files, one JSON record per
  line with a CRC32 trailer. Fault injection: `crashAfterAppends` (storage dies mid
  rollback/abort) and `tornNextAppend` (half-written record, e.g. a torn checkpoint
  end). Corrupt lines are reported as anomalies and skipped on reload.
- **Transactions** (`src/txn.ts`): savepoints bind a name to the txn-internal LSN
  (reusing a name overwrites the binding). `rollbackTo` walks the txn chain backwards,
  compensating only `update` records after the savepoint and skipping existing CLRs via
  `undoNext` — so repeated rollback to the same savepoint is a no-op and a full `abort`
  never re-undoes compensated updates. The transaction stays active and can commit.
- **Locks & visibility** (`src/locks.ts`, `src/pagestore.ts`): strict transaction-level
  exclusive page locks held to commit/abort (no-wait conflict detection). External
  readers only see the committed version; uncommitted buffer state never leaks.
- **Recovery** (`src/recovery.ts`): analysis (from the last *complete* checkpoint, or
  log start), redo (repeats history from the oldest dirty-page recLSN), undo (losers,
  highest-LSN-first, CLR-aware, writes CLRs + `end`).
- **Truncation** (`src/truncate.ts`): retention boundary =
  `min(oldest active txn firstLSN, oldest dirty-page recLSN, last complete checkpoint
  beginLSN)`, with per-factor rationale. Truncation is refused when no complete
  checkpoint exists or the requested point exceeds the safe boundary.
- **Independent rebuild** (`src/rebuild.ts`): re-derives final committed pages from the
  full log by separate logic (committed txns replayed as logged; loser chains walked
  backwards with undoNext, compensations applied highest-LSN-first) and is compared
  against recovery from the truncated log in tests and in the CLI.

## Layout

```
src/    types, crc, filelayer, log, pagestore, locks, txn, recovery, truncate, rebuild, cli
test/   savepoint, abort+locks, recovery, truncation (node:test)
run/    CLI scenario output directories (real segment files + pages.json)
```
