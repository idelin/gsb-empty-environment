# dagsched

A Go build-scheduling library that runs independent tasks on a DAG.

## Semantics

- **Dependency gating** — a task starts only after all its dependencies
  have succeeded.
- **Failure-scoped skipping** — a failed task marks exactly its transitive
  dependents `skipped`; sibling branches keep running. Skipped tasks carry
  a `*DepError` chain that unwraps hop by hop to the root failure, so
  `errors.Is` / `errors.As` work across the whole chain.
- **All-or-nothing resources** — a task starts only when its full
  `Needs` request fits in the remaining capacity, holds the grant for its
  whole run, and releases it on completion. Requests are validated against
  capacity at `Add` time.
- **Cancellation** — canceling the context stops admitting new tasks
  (unstarted tasks become `canceled`) and `Run` returns only after every
  already-started task has finished its cleanup. `Run` then returns
  `ctx.Err()` alongside the per-task results.
- **Determinism** — dispatch order follows task registration order, so a
  fixed scenario yields identical terminal states on every run.

## Verification

Tests drive the scheduler with a scripted virtual executor
(`virtexec_test.go`) that instruments every run: resource accounting with
capacity-violation detection, start/cleanup order recording, and
one-shot gates for deterministic interleavings. Covered invariants:

- terminal states are deterministic across repeated runs;
- failure reason chains unwrap from a skipped leaf to the root cause;
- resource conservation: usage never exceeds capacity and every grant is
  released (also under cancellation and panics).

```sh
go test -race ./...
```
