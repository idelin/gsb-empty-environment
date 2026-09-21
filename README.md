# tasksched

Failure-driven retry scheduler in pure Go stdlib.

- Explicit backoff sequence; last entry repeats when attempts outnumber it.
- Retries reuse the same task ID with a 1-based incrementing attempt number.
- Cancel vs. timer-expiry race resolves to exactly one result (`select`).
- Cancel during backoff returns `ctx.Err()` immediately; the pending timer
  is stopped so no late timer can restart a completed task.
- On exhausting `maxAttempts`, returns the last error plus the full
  attempt history (`Outcome.Attempts`).
- Time is injected via the `Clock` interface; tests use `ManualClock`,
  production uses `RealClock`.

## Run tests (with race detector)

    go test -race -v ./...

## Run the demo

    go run ./demo
