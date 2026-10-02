# mvcc: version chains + ReadView-tracking GC

A minimal MVCC (multi-version concurrency control) store in Go, stdlib only.

- Every write prepends a new `Version` to the head of a per-key chain,
  linked newest-first by commit timestamp.
- Reads walk the chain and return the first version with `TS <= readTS`.
- The store tracks all active read views; the GC horizon is the global
  minimum active read-view timestamp (or the latest commit timestamp
  when no reader is active).
- `GC()` keeps, per chain, the newest version visible at the horizon and
  unlinks every older version: they are covered and can never be observed
  by any active or future reader, so the Go GC reclaims their memory.

## Layout

- `store.go` — `Store`, `Version`, `ReadView`, `GC`
- `store_test.go` — acceptance + edge-case tests
- `cmd/demo/main.go` — runnable walkthrough of the acceptance scenario

## Run

```sh
go test ./...        # or: go test -race ./...
go run cmd/demo/main.go
```

## Acceptance scenario

Item X has v1(t=1), v2(t=5), v3(t=10); the global minimum active
ReadView is t=8. `GC()` prunes v1 (covered by v2, which is visible at
t=8), keeps v2 and v3, and read-only queries at t=8 and t=10 still
return v2 and v3 respectively.
