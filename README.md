# waitfirst

`WaitFirst` registers several one-shot events at once, returns the index
of the first one to complete, and revokes every other registration.

```go
idx, err := waitfirst.WaitFirst(ctx, []waitfirst.Event{a, b, c})
```

## Semantics

- **First completion wins.** The result is the index of the first event
  observed completed.
- **Ties break by input order.** If several events have completed by the
  time the decision is made — including events that completed *while the
  others were still being registered* — the lowest index wins.
- **No missed completions.** Events that fire before or during
  registration are still observed: `Subscribe` on an already-completed
  event runs the callback synchronously, and every wakeup re-scans all
  events from index 0.
- **External cancellation.** A cancelled context returns `-1` and
  `ctx.Err()`; a completion racing with cancellation still wins (a final
  scan runs before the error is returned).
- **No leftover subscriptions.** Every registration is revoked before
  `WaitFirst` returns, including the winner's (already consumed) one.

## How it works

`WaitFirst` subscribes a callback to every event in input order. Each
callback sets a per-event `atomic.Bool` and nudges a buffered wake
channel. The waiter loops: scan all flags in index order (lowest
completed index wins), then park on `select` over the wake channel and
`ctx.Done()`. A deferred pass unsubscribes every registration. The
algorithm is confluent: the decision depends only on the set of
completions visible at scan time, never on channel timing.

`OneShot` (in `oneshot.go`) is a ready-made `Event`: `Fire` completes it
once and delivers pending callbacks; `Pending` exposes the number of
registrations neither delivered nor revoked, which the tests use to prove
no subscription leaks.

## Verification

### Schedule enumeration (`model_test.go`)

`internal/sched` is a deterministic cooperative scheduler: threads yield
only at explicit `Step`/`BlockUntil` points, and `Explore` re-runs a
program once per possible schedule, enumerating the interleaving tree
depth-first (with CHESS-style preemption bounding for larger
configurations).

`model_test.go` models `WaitFirst` step-for-step — one thread per event
source, one cancellation thread, one waiter thread mirroring the real
registration/scan/teardown phases — and asserts after **every** explored
schedule:

- no callback ever runs twice;
- no subscription is left behind after return;
- the returned index is the lowest one completed at decision time
  (completed events are never missed, ties break by input order);
- a cancellation result is only reported when no completion was
  observable.

Configurations with 1–2 events are explored exhaustively; 3–4 events use
preemption bounds of 3 and 2. The `internal/sched` package has its own
unit tests pinning down the enumeration, blocking, and deadlock
detection.

### Real-code tests (`waitfirst_test.go`)

Unit tests cover pre-fired events, ties, completion during registration,
cancellation (including completion racing cancellation), unsubscribe
behavior, and a 2000-iteration randomized stress test. Run everything
with the race detector:

```sh
go test -race ./...
```
