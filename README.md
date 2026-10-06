# memtx

In-memory table with snapshot-isolation transactions and unique secondary
indexes (Go 1.11+).

## Semantics

- `Begin` fixes a read snapshot; `Get` sees that snapshot overlaid with the
  transaction's own buffered writes.
- `Commit` validates the **final write set**, so one transaction may swap
  two rows' unique keys, or delete a row and reuse its key.
- Commit detects write-write conflicts (`ErrWriteConflict`) and unique-key
  ownership races (`ErrUniqueConflict`) against anything committed after the
  snapshot; genuine duplicates fail with `*DuplicateKeyError`.
- Validation completes before any mutation: a failed commit never partially
  modifies the main table or any index.

## Tests

- `go test -short` — targeted unit tests (swap, delete-and-reuse, conflict
  classification, snapshot reads, atomic failure).
- `go test` — additionally enumerates all 157 transaction programs of up to
  two ops over a 3-key x 3-value universe, runs every ordered pair under
  three schedules (sequential, concurrent A→B, concurrent B→A), and after
  each of ~74k runs checks the main table against a replay of committed
  programs, rebuilds and compares the unique indexes, and verifies the
  post-commit snapshot.
