# batchagg

A Go write aggregator: buffers individual items and flushes them to a
`Sink` in batches, either when a batch reaches `Options.MaxBatch` items or
when an explicit barrier (`Flush`) is requested.

## Semantics

- **Independent receipts** — every `Submit` returns a `Receipt` that
  resolves exactly once with that item's own outcome (`Receipt.Err()`).
- **Short writes** — `Sink.WriteBatch` reports how many prefix items it
  wrote. Those items succeed; the rest fail with the sink's error, or with
  `ErrShortWrite` if the sink returned a short count with a nil error.
- **Barriers don't lie** — `Flush` reports the combined outcome of every
  item submitted since the previous barrier, including items already
  flushed by the count trigger. A failed batch is never hidden from a
  later barrier.
- **Close** — stops accepting new items (`Submit`/`Flush` return
  `ErrClosed`), waits for any in-flight flush, flushes the remaining
  buffer, and completes every outstanding receipt.

## Example

```go
agg := batchagg.New[string](sink, batchagg.Options{MaxBatch: 100})
rec, err := agg.Submit("event")
// ... rec.Err() blocks until the item's batch is flushed.
if err := agg.Flush(); err != nil { /* barrier over recent items failed */ }
err = agg.Close() // flushes the rest, completes all receipts
```

## Tests

`aggregator_test.go` drives a fully orchestrable `gateSink`: each
`WriteBatch` announces its batch on a channel and blocks until the test
supplies the result, so batch boundaries, partial success, and close
contention are verified deterministically with no sleeps.

```
go test -race ./...
```
