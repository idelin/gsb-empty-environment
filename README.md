# lsm — MVCC-aware LSM compaction (simplified)

A simplified LSM-tree compactor in Go (standard library only). Records are
versioned by sequence number and may be tombstones; readers pin snapshots;
compaction selection/build is fully separated from atomic manifest publish.

## Layout

- `record.go` — `Record{Key, Seq, Value, Tombstone}` and immutable sorted `Segment`.
- `merge.go` — per-key version merge: identical duplicates collapse, same
  `(Key, Seq)` with different content is `ErrCorruption`; snapshot-aware GC.
- `compact.go` — `Compaction` selection inputs (segments, active snapshots,
  lower-level key ranges), `Build` (no side effects), `RunCompaction`
  (build + atomic publish).
- `manifest.go` — atomic `Commit(expect, add, remove)`; failed commits
  change nothing, so old segments stay readable until the swap.
- `read.go` — range/binary-search `Reader`, and `ReferenceReader` which
  full-scans every record of every segment as ground truth. Both count the
  records they actually examine (`Stats.RecordsRead`).
- `lsm_test.go` — correctness tests, all verified against the reference.
- `cmd/lsmdemo` — deterministic replayable end-to-end scenario.

## Correctness rules

Versions of a key are ordered by `Seq` descending. A version is kept iff:

1. it is the newest version (future readers), or
2. an active snapshot `s` falls in `[v.Seq, nextNewer.Seq)` — exactly the
   version `s` observes.

A tombstone is dropped only when it is the oldest kept version of its key
**and** no lower-level key range covers the key (range-based, conservative
proof — actual lower-level records are never inspected). A tombstone with
kept older versions beneath it is never dropped, otherwise those versions
would resurrect for future readers. Output segments never overlap: splits
happen only at key boundaries.

## Run

```sh
go test ./...        # correctness tests (no timing-based assertions)
go run ./cmd/lsmdemo # deterministic replayable demo
```

## Tests

- `TestOldReaderSurvivesDeletion` — snapshot pinned across a delete keeps
  both the old value and the tombstone; old and new readers both correct.
- `TestNewWriteMergesWithTombstone` — value/tombstone/value stack merges;
  unneeded middle tombstone collected, snapshot version preserved.
- `TestRangeOverlapWithoutActualKeys` — lower range covers the key but has
  no record for it: tombstone conservatively kept; without lower coverage
  it is dropped. Reads identical either way.
- `TestSequenceConflict` — same key+seq, different content → `ErrCorruption`;
  identical duplicates merge silently.
- `TestPublishFailure` — injected manifest failure leaves the old live set
  fully readable; retry commits atomically.
- `TestRandomizedAgainstReference` — seeded random workload, every
  (key, snapshot) read checked against the full-scan reference, with
  record-read counts asserted.
