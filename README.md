# roundrendezvous

A round-based rendezvous (cyclic barrier) for a dynamic set of participants,
built only on the Go standard library (`sync.Mutex`, per-round channels,
`context`).

## Semantics

- `rendezvous.New(parties, action)` creates a barrier; `action` is injected
  by the caller and runs once per round before waiters are released.
- `Arrive(ctx)` blocks until the round completes and returns the round's
  error. Cancelling `ctx` means the caller leaves the barrier.
- `Leave()` removes a non-waiting participant; if the remaining arrivals
  already satisfy the reduced requirement, the round is released (no
  premature release, no permanent wait).
- `Add(n)` schedules new participants to join from the next round; the
  current round's expectation is untouched.
- A failing `action` or an explicit `Fail(err)` releases every waiter of the
  current round with that error; the next round starts clean.
- Arrivals reaching the barrier while the previous round is still finishing
  wait for the next round and are never counted toward the finishing one.

## Run

```sh
go test -race ./...   # tests, including 1000-round crosstalk check
go run ./cmd/demo     # runnable sample
```
