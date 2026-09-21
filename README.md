# taskscope

A small Go library (standard library only) for a group of subtasks bound to
one cancellable **scope**:

- Subtasks belong to the parent scope and inherit its `context.Context`.
- The first non-cancellation failure cancels all sibling subtasks
  (`context.WithCancelCause`, the failure becomes `context.Cause`).
- `Wait` blocks until **every** subtask has exited — including a sibling that
  is still running cleanup after being cancelled. It cannot return early.
- Errors are aggregated with `errors.Join`: every original cause is
  preserved and reachable via `errors.Is` / `errors.As`; cancellation errors
  (`context.Canceled`) are filtered out, so cancellation never overwrites the
  first failure.
- Nested scopes are derived from the parent context, so parent cancellation
  propagates down; no goroutines are leaked.
- `Cancel` and `Wait` are idempotent and safe to call repeatedly/concurrently.

Only standard synchronization primitives are used: `sync.WaitGroup`,
`sync.Mutex`, and `context`.

## API

```go
s := taskscope.New(parentCtx)        // nil parent => context.Background
s.Go(func(ctx context.Context) error { ... }) // subtask inherits scope ctx
s.Cancel()                           // explicit cancel; repeat-safe
err := s.Wait()                      // waits for full cleanup; joined errors
s.Ctx()                              // scope context, for nesting
```

A subtask that observes `ctx.Done()` finishes its cleanup and returns
`ctx.Err()` (or `nil`); that result is not collected by `Wait`.

## Run the sample

```
$ go run ./cmd/demo
```

```
== flat scope: failure cancels sibling, Wait waits for cleanup ==
subtask A: failing
subtask B: cancelled, starting cleanup ...
subtask B: cleanup done
Wait returned after 172ms: boom: injected failure

== nested scope: parent failure propagates to the child ==
nested child: cancelled by parent, cleaning up ...
root Wait: parent: injected failure

repeated explicit cancellation is safe:
Wait on cancelled empty scope: <nil>
```

## Tests

Failure timing, cleanup duration and injected errors are all driven by the
tests (see `scope/scope_test.go`).

```
$ go test -race -v ./scope/
```

```
=== RUN   TestFailureCancelsSiblingsAndWaitsCleanup
--- PASS: TestFailureCancelsSiblingsAndWaitsCleanup (0.10s)
=== RUN   TestAggregateErrorsKeepsCauses
--- PASS: TestAggregateErrorsKeepsCauses (0.04s)
=== RUN   TestRepeatedCancelAndWaitSafe
--- PASS: TestRepeatedCancelAndWaitSafe (0.00s)
=== RUN   TestCancelBeforeGo
--- PASS: TestCancelBeforeGo (0.00s)
=== RUN   TestAllSuccessNil
--- PASS: TestAllSuccessNil (0.00s)
=== RUN   TestNestedScopesPropagateAndDoNotLeak
--- PASS: TestNestedScopesPropagateAndDoNotLeak (0.06s)
=== RUN   TestEmptyScopeAndGoAfterWait
--- PASS: TestEmptyScopeAndGoAfterWait (0.00s)
PASS
ok  	scope-demo/scope	1.832s
```

Also verified with `go test -race -count=50` (50 repeated runs) with no
flakes and no data races.
