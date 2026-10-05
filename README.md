# lsmcompact — simplified LSM compactor with MVCC snapshots

Standard-library-only Go implementation of a versioned-record compactor:
records carry `key`, `seq`, and a value or tombstone; segments are
immutable sorted runs ordered by `(key asc, seq desc)`.

## Layout

- `lsm/record.go` — `Record`, `Segment`, ordering validation.
- `lsm/compact.go` — compaction selection (`SelectCompaction`), k-way
  merge, snapshot-aware GC, tombstone reclamation, corruption detection.
- `lsm/manifest.go` — immutable `Version`s and atomic `Manifest.Commit`
  with failure injection; old versions stay readable forever.
- `lsm/reader.go` — indexed point reads with record-read accounting
  (`Stats`) plus `ReferenceGet`, a full-scan MVCC oracle.
- `cmd/lsmdemo` — deterministic replayable end-to-end scenario.

## Correctness rules

- Same key, same seq, different content → `ErrCorruption`; identical
  duplicates dedupe silently.
- A version is kept iff it is the newest for its key or some pinned
  snapshot still sees it.
- A tombstone is dropped only when (1) it is the oldest retained version
  of its key, (2) lower levels provably hold no older value (`Coverage`:
  `RangeCoverage` is conservative, `ExactCoverage` is precise), and
  (3) no pinned snapshot sits at/above its seq. Being the newest record
  is never, alone, grounds for removal.
- Selection (`SelectCompaction`), execution (`RunCompaction`) and
  publication (`Commit`) are separate; a failed commit leaves the old
  manifest untouched and readers observe no change.
- Compaction outputs are split only at key boundaries, so output
  segments never overlap in key range.

## Run

```sh
go test ./...                 # unit + randomized MVCC verification
go test ./lsm -run Replayable -seed 42   # replay with another seed
go run ./cmd/lsmdemo          # deterministic end-to-end demo
```

Tests assert on actually-read record counts (`Stats.RecordsRead`)
against the full-scan reference — never on wall-clock timing.
