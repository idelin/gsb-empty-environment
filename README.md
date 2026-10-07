# txnsched

Scheduler for a half-duplex device that allows **exactly one in-flight
request** and may deliver responses **later than the request timeout**.
Transaction numbers come from a small, wrapping space (e.g. one byte).

## Design

- **Event loop**: a single goroutine owns all state (queue, in-flight
  exchange, allocator). Submissions, device responses, timeouts,
  cancellations and shutdown are events on one channel, so there are no
  data races and replay is deterministic.
- **One in-flight**: a queued request is dispatched only when no
  exchange is on the line. Completion, timeout or a transport error
  immediately dispatches the next queued request.
- **Isolation (quarantine)**: when an exchange ends — response, timeout
  or send failure — its transaction number is quarantined for
  `Config.Isolation` and cannot be reused before that. If every number
  is isolated, dispatch stalls and a wakeup timer retries when the
  earliest quarantine expires.
- **Late responses can never complete a new request**: responses are
  matched strictly against the in-flight transaction number; anything
  else is dropped and counted in `Stats.LateResponses`. Because a number
  cannot be reused while isolated, a late answer finds no matching
  exchange — provided the invariant below holds.

## Invariant

`Config.Isolation` must be **at least the maximum lateness** of a device
response. If a response can arrive later than the isolation window, its
number may already have been reused and the stale answer would be
indistinguishable from the answer to the new exchange.

## Cancellation & shutdown

- Cancelling a **queued** request's context removes it from the queue;
  the in-flight exchange is unaffected.
- Cancelling the **in-flight** request's context unblocks its caller
  with the context error, but the device exchange runs on until a
  response or timeout — the device cannot be interrupted.
- `Close` rejects new submissions (`ErrClosed`), drains the queue with
  `ErrShutdown`, lets the in-flight exchange finish, then exits;
  `Wait` blocks until the loop is done.

## Virtual clock

`Clock` abstracts time. `RealClock` wraps the wall clock;
`VirtualClock` is advanced manually and fires timers synchronously, in
deadline order, so tests replay exact interleavings of timeout,
wraparound, shutdown drain and late responses without sleeping.

## Tests

```sh
go test -race ./...
```

Replay coverage: timeout → quarantine, number wraparound blocked by
isolation, late/duplicate responses dropped, single-number space
serialization, graceful shutdown drain (by response and by timeout),
queue vs. in-flight cancellation, transport failure, submit-after-close.
