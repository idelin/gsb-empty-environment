# Grouped Retractable Top-K

Python 3.11, standard library only, verified with `unittest`.

Per-group retractable Top-K over a stream of local change events
(`txn_id`, `table`, `pk`, `before`, `after`, `commit_seq` — plain JSON
fixtures, no message platform). Deletions backfill from a reserved
candidate buffer; overflow candidates live in external on-disk segments,
so backfill never degrades into a full group scan.

## Layout

- `topk/keys.py` — sort-key encoding: multi-field specs `(name, desc,
  nulls_first)`, null placement, primary key appended as the stable
  tie-breaker.
- `topk/storage.py` — `BlockStore`: one file per spilled segment;
  counts every block read/write.
- `topk/engine.py` — `GroupedTopK` (per-table engine), `Snapshot`
  (version-pinned pagination), `Pipeline` (routes events by table,
  applies them in `commit_seq` order).
- `topk/reference.py` — independent full-sort oracle (own comparator,
  sorts whole groups) used to verify the incremental engine.
- `topk/fixtures.py` — deterministic local fixture generator/loader.
- `run_demo.py` — end-to-end sample run; `test_topk.py` — test suite.

## How it works

- Each group keeps a sorted in-memory window of the best `k + buffer`
  candidate keys. Beyond `k + buffer + slack`, the tail is spilled to an
  immutable sorted segment in the `BlockStore`; in-memory metadata
  `(min_key, max_key, bid, size)` indexes every segment.
- A delete removes the key from memory, or tombstones it if it sits in a
  segment (lazy, O(1) blocks). Backfill comes from the buffer; a segment
  is merged in only when its `min_key` overlaps the current top window —
  a bounded k-way merge, never a full scan of the group.
- The global `pk -> (group, key)` index makes updates and group
  migration single-entry operations: re-keying or re-grouping a pk
  never produces a duplicate.
- Every commit bumps a version, appends changed tops to a per-group
  version log, and emits a stable-id delta `{group: {added, removed}}`
  (an entry with empty lists signals a pure re-ordering). `snapshot()`
  pins the current version; `page(group, offset, limit)` reads the
  pinned top regardless of later commits. `k = 0` tops stay empty.
- `checkpoint()` / `GroupedTopK.recover(dir)` persist and restore the
  full state (indexes, windows, segment manifest, version logs, block
  counters).

## Run

```sh
python3.11 run_demo.py     # sample run against fixtures/changes.json
python3.11 -m unittest test_topk -v
```

The suite covers: rank-1 delete backfilling the K+1-th candidate,
K=0 always empty, group migration leaving the old group, duplicate-free
re-keying, multi-field order with null placement, id tie-break at the
last rank, version-pinned snapshot pagination, stable-id deltas,
hot-group skew vs the full-sort oracle, tiny-memory spilling with block
accounting, bounded (non-full-scan) backfill, and checkpoint/recovery.
