# batchwriter

Buffers individual write requests and flushes them to a store in batches.

## Flush semantics

A batch is cut and written when either:

- it reaches `MaxSize` buffered requests, or
- the **oldest** buffered request has waited `MaxDelay`.

The deadline is anchored to the oldest request: new arrivals join the
current batch but never extend its deadline, so a steady stream of new
requests cannot starve old ones.

## Guarantees

- **Exactly-once delivery of results**: every `Request` resolves exactly
  once via `Done()` / `Wait()` — with `nil`, the batch's write error,
  `ErrCancelled`, or `ErrClosed`.
- **Failure fan-out**: if `Write` fails, that error is delivered to every
  member of the failed batch; other batches are unaffected.
- **Cancel**: `Cancel` removes a request only while it is still buffered.
  Once its batch has been cut, `Cancel` returns `false` and the item is
  written.
- **Atomic cut**: the batch boundary is taken under one lock, so a request
  is either in the cut batch or the next one — never both, never lost.
- **Ordered writes**: concurrent flushes are serialized so batches reach
  `Write` in cut order.

## Virtual time

`Config.Clock` accepts a `FakeClock` for deterministic tests. Time moves
only via `Advance`, and timers scheduled for the same tick fire in
registration (FIFO) order. This pins down the batch boundary when a flush
and an enqueue land on the same tick: whichever is ordered first wins, the
partition is stable, and no item is written twice. See
`TestSameTickFlushAndEnqueue` and `TestEnqueueDuringFlush`.

## Usage

```go
b := batchwriter.New[Item](batchwriter.Config[Item]{
    MaxSize:  100,
    MaxDelay: 50 * time.Millisecond,
    Write: func(ctx context.Context, items []Item) error {
        return store.InsertAll(ctx, items)
    },
})
req := b.Submit(item)
if err := req.Wait(); err != nil { /* batch error, ErrCancelled, or ErrClosed */ }
```

## Test

```sh
go test -race ./...
```
