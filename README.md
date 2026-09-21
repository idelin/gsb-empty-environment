# retrysched

Failure-driven task retry scheduler (Go, standard library only).

- Explicit backoff sequence: `backoff[i]` is the wait before attempt `i+2`;
  a task runs at most `len(backoff)+1` attempts.
- Retries keep the same task ID with incrementing attempt numbers.
- Cancel vs. expiring retry timer: exactly one terminal `Result` is produced
  (state machine guarded by a single mutex).
- Late timers never start an attempt after completion.
- Exhaustion returns the last error plus the full attempt history.
- Cancel during a backoff wait stops the timer and returns immediately.
- Time is injected via the `Clock` interface; `ManualClock` drives tests
  without real sleeps.

## Run

```sh
go test -race ./...   # tests (manual clock, no real waiting)
go run ./cmd/demo     # live demo with the real clock
```
