# memtx

In-memory transactional key-value store for Go with snapshot isolation.

## Semantics

- **Fixed snapshot**: `Begin` pins the transaction to the current committed
  revision. The snapshot never advances; commits by other transactions
  afterwards are invisible to it.
- **Read-your-own-writes**: `Put`/`Delete` buffer into a per-transaction
  write set. `Get` consults the write set first, then the snapshot.
- **Merged range scans**: `Scan` merges the snapshot with the write set in
  sorted key order — buffered puts appear at their sorted position,
  buffered deletes mask snapshot entries, overwrites show the buffered
  value. Range bounds apply to both sources.
- **Revision-bound cursors**: paginated scans return a `Cursor` recording
  `(snapshot revision, write-set revision)`. Any `Put`/`Delete` bumps the
  write-set revision, so reusing an old cursor fails with
  `ErrStaleCursor` — a paginated reader never observes a shifting view.
- **Optimistic commit**: `Commit` applies the whole write set at one new
  database revision. If another transaction committed any of the written
  keys after this transaction's snapshot, `Commit` fails with
  `ErrConflict` and the transaction is aborted.

## Example

```go
db := memtx.Open()

tx := db.Begin()
tx.Put("a", []byte("1"))
tx.Delete("b")

page, _ := tx.Scan("", "", 100, nil) // first page
for page.Next != nil {
    page, _ = tx.Scan("", "", 100, page.Next) // ErrStaleCursor if write set changed
}

if err := tx.Commit(); err == memtx.ErrConflict {
    // retry with a fresh transaction
}
```

## Testing

`model_test.go` cross-checks the library against an independent reference
model that materializes the full snapshot + write-set merge for every
operation, driving both in lockstep with randomized workloads (begin /
put / delete / get / paginated scan / stale-cursor probe / commit /
rollback) and reconciling the final committed state.

```sh
go test -race ./...
```
