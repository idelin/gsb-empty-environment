# dagsched

A Go (1.11+) library that runs a DAG of independent tasks with
all-or-nothing resource admission.

## Semantics

- **DAG execution**: a task starts only after all its dependencies
  succeed. Cycles, self-dependencies and unknown dependencies are
  rejected by `Run`.
- **Failure propagation**: a failed task skips exactly its transitive
  dependents (`StateSkipped`); unrelated branches keep running. The
  skip reason is a `*DepError` chain — use `Chain(err)` / `RootCause(err)`
  to walk back to the root failure.
- **All-or-nothing resources**: a task starts only when its full
  `Needs` fit in currently free capacity; requests are never partially
  granted, and everything is released when the task returns. Ready
  tasks are considered in sorted ID order, so admission is deterministic.
- **Cancellation**: canceling the context stops new admissions
  (pending tasks become `StateCanceled`) and `Run` blocks until
  already-started tasks return, giving them time to clean up.
- **Deterministic terminal states**: every task ends in exactly one of
  `succeeded`, `failed`, `skipped`, `canceled`.

## Virtual executor

`Executor` (virt.go) simulates execution for tests: scripted work,
failures, cancel-aware cleanup, an ordered event log, and independent
tracking of held resources to verify conservation (peak ≤ capacity,
zero leaked at the end).

```sh
go test ./...
```
