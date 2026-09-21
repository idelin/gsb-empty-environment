# scopex

Structured-concurrency scopes for Go, standard library only
(`context`, `errors`, `sync`).

## Semantics

- **Subtasks belong to their parent scope.** `Scope.Go` spawns a subtask
  that receives the scope's context; `Scope.Scope` spawns a nested scope
  derived from the parent's context.
- **One failure cancels the siblings.** The first non-nil error returned
  by a subtask becomes the scope's cause (`context.WithCancelCause`) and
  cancels the scope context, so every sibling observes it and can clean up.
- **Exit waits for cleanup.** `Scope.Wait` blocks until every subtask —
  including ones still cleaning up after a sibling's failure — has fully
  returned. Nested scopes wait for their own subtasks before reporting
  upward, so no goroutine escapes the parent's `Wait`.
- **First failure wins, causes are preserved.** Cancellation reported by
  siblings during cleanup is dropped once a failure exists and can never
  override it. Later distinct failures are aggregated with `errors.Join`,
  so the result unwraps to every original cause. A cause propagated back
  through a nested scope's context is de-duplicated, never double-counted.
- **Repeated cancellation is safe.** `Cancel` is idempotent; the first
  cause is retained forever, even across calls after `Wait`.

## Usage

```go
err := scopex.Run(ctx, func(s *scopex.Scope) error {
    s.Go(func(ctx context.Context) error { ... }) // subtask
    s.Scope(func(child *scopex.Scope) error {     // nested scope
        child.Go(func(ctx context.Context) error { ... })
        return nil
    })
    return nil
})
```

Explicit lifetime control:

```go
s := scopex.New(ctx)
s.Go(task)
s.Cancel() // idempotent
err := s.Wait()
```

## Example

```
$ go run ./cmd/example
fetch: failed!
store: canceled, cleaning up...
parse: canceled, cleaning up...
parse: cleanup done
store: cleanup done
Run returned after 301ms, err=fetch: boom
```

`fetch` fails at 200ms; `parse` and the nested `store` are canceled and
finish their 100ms cleanup before `Run` returns at ~300ms with the first
failure.

## Tests

Task functions, timing, and failures are injected by the tests (channels
and closures, no real sleeps on the critical path). Run with the race
detector:

```
$ go test -race -count=1 -v ./...
=== RUN   TestAllSubtasksSucceed                        --- PASS
=== RUN   TestFailureCancelsSiblings                    --- PASS
=== RUN   TestWaitBlocksUntilSiblingCleanupFinishes     --- PASS
=== RUN   TestFirstFailureWinsAndAggregates             --- PASS
=== RUN   TestCancellationDoesNotOverrideFailure        --- PASS
=== RUN   TestNestedScopesPropagateAndCleanUp           --- PASS
=== RUN   TestNestedScopesDoNotLeakGoroutines           --- PASS
=== RUN   TestNestedScopeDoesNotDuplicateParentFailure  --- PASS
=== RUN   TestRepeatedCancelIsSafe                      --- PASS
=== RUN   TestParentCancellationPropagates              --- PASS
=== RUN   TestAggregationPreservesEveryCause            --- PASS
=== RUN   TestRunIncludesBodyError                      --- PASS
PASS
ok  	scopex	1.4s
```

Also verified stable with `go test -race -count=5 ./...`.
