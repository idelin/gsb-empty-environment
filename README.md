# memtx

In-memory transactional key-value store for Go.

## Semantics

- **Fixed commit snapshot** — `Store.Begin` pins the transaction to the
  current commit sequence. All reads resolve against per-key version
  chains at that sequence; later commits are invisible to the transaction.
- **Read-your-writes** — `Put`/`Delete` buffer into a private write set.
  `Get` consults the write set before the snapshot.
- **Merged range scans** — `Txn.Scan` streaming-merges the sorted snapshot
  entries with the sorted write set: inserts override/extend committed
  data, tombstones suppress committed keys, output stays key-ordered.
- **Revision-bound cursors** — each write-set mutation bumps a revision.
  Page cursors carry the revision at issue time; a `Scan` resumed with a
  cursor older than the current revision fails with `ErrStaleCursor`.
- **Optimistic commit** — `Commit` applies the write set atomically under
  one new commit sequence, rejecting with `ErrConflict` if any written key
  was committed by another transaction after this one's snapshot
  (first-committer-wins).

## Verification

`model_test.go` cross-checks the streaming implementation against a
reference model (snapshot copy + write set, fully materialized merge) over
randomized operation sequences and multiple seeds: identical `Get`
results, identical scan pages under random ranges/page sizes, identical
cursor issuance, and identical stale-cursor rejection.
