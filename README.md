# batsim

Battery simulation library in Go using exact rational arithmetic
(`math/big.Rat`) — no floating-point drift, conservation identities hold
exactly.

## Single battery (`battery.go`)

Each `Step(cmd, dt)`:

1. Applies self-discharge first (`Stored *= 1 - rate`), booked into
   `TotalSelfDischargeLoss`.
2. Applies the charge **or** discharge command; a command requesting
   both is rejected (`ErrSimultaneousChargeDischarge`) before any
   mutation.
3. Clips to power limits and to `[0, Capacity]`, marking
   `StepResult.Truncated`.

Losses are accounted in separate books (`TotalSelfDischargeLoss`,
`TotalConversionLoss`), so the exact identity

```
Stored + SelfLoss + ConvLoss + GridOut == InitialStored + GridIn
```

holds at every step (`Battery.ConservationError()` returns the residual).

## Fleet with shared charge limit (`fleet.go`)

`Fleet.ExecuteBatch` allocates the shared charging power budget to a
batch of commands atomically (greedy, in command order), then executes.
Executed commands are never rolled back: an invalid command in a batch
is recorded with its error and skipped while the rest of the batch
stands. Every executed action is appended to the immutable
`Fleet.ActionLog`, which can be replayed to recover the exact state.

## Estimator (`estimator.go`)

`Estimator` keeps a versioned sensor log, strictly separated from
execution state. `Revise` appends a late reading, replays the full
version log (latest version per battery wins), and returns the
per-battery delta against the previous estimate. Revisions never touch
batteries, energy books, or the action log.

## Tests

```
go test ./...
```

Covers boundary truncation (power/capacity/empty/full), rejection of
simultaneous charge+discharge, self-discharge ordering, a 2000-step
random sequence with per-step conservation checks and exact replay
recovery, shared-limit batch allocation with no rollback, and
estimator/execution separation with deterministic version replay.
