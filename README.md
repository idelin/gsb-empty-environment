# mpqueue

Bounded multi-producer multi-consumer FIFO queue for Go with two distinct
shutdown modes, an auditable operation history, and conservation checks.

## Shutdown semantics

- `Close()` — graceful: producers are rejected (`ErrClosed`); consumers keep
  draining buffered items and see `ErrClosed` only once the queue is empty.
- `Abort()` — failure: everything stops immediately; blocked and future
  consumers see `ErrAborted`; remaining buffered items are returned to the
  `Abort` caller in FIFO order (ownership transfer).
- The two may race: the first to take effect wins and fixes the semantics;
  the loser is a no-op and reports `won == false`.

## Cancellation never swallows elements

`Enqueue`/`Dequeue` take a `context.Context`. A dequeue cancelled while
waiting takes no item; an enqueue cancelled while waiting for capacity keeps
its item with the producer. Cancelled/rejected operations never enter the
history.

## Auditability

- `History()` returns the committed operation log (linearization order,
  contiguous `Seq`).
- `Verify(hist, cap)` audits capacity and ownership conservation:
  `enqueued == dequeued + released + remaining`, buffer within
  `[0, cap]`, first-shutdown-wins, no dequeue after abort.
- `Replay(hist, cap)` re-executes the log on a model queue, checks FIFO
  order, and reconstructs the final buffer and abort remainder.
- `Check()` verifies the same invariants on the live queue; `Stats()`
  exposes the counters.

## Test

```sh
go test -race ./...
```
