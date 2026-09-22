# dagscheduler

A small, standard-library-only Go DAG task scheduler.

- Tasks run only after **all** prerequisites have succeeded.
- A failed task marks every descendant `Blocked` (the function is never called);
  the blocked state cascades, while **unrelated branches keep running**.
- Diamond merge nodes execute **exactly once**.
- Cycles are rejected **before execution** and when edges are added at runtime;
  the error carries the closing node chain, and the scheduler never hangs on a
  cycle.
- Dependencies can be added dynamically under a restricted rule.
- Built only with standard synchronization primitives (`sync.Mutex`,
  `sync.Cond`, `sync.WaitGroup`, channels); passes `go test -race`.

## Layout

- `dagscheduler/scheduler.go` — scheduler implementation.
- `dagscheduler/scheduler_test.go` — table/channel-injected tests.
- `cmd/demo/main.go` — runnable example showing failure + blocking.

## API sketch

```go
s := dagscheduler.New()
s.Add("root", rootFn)
s.Add("child", childFn, "root")              // prerequisites must exist
err := s.AddDependency("child", "other")      // dynamic edge
results, err := s.Run(context.Background())
```

Statuses: `Pending`, `Running`, `Succeeded`, `Failed`, `Blocked`.

### Dynamic dependency rule

- Before `Run`: `AddDependency` always succeeds (unknown nodes / cycles still
  error).
- During `Run`: an edge onto a task is accepted only while that task is still
  `Pending`; once it is `Running` the call returns `ErrRunning`. This keeps the
  set of prerequisites stable once a task begins.
- An edge that would close a cycle returns `*CycleError` and leaves the graph
  unchanged. Re-adding an existing edge is a no-op.

### Cycles

`CycleError.Chain` closes on itself, e.g. adding `A -> C` to `A <- B <- C`
reports `[A C B A]`. `Run` performs a pre-flight check (iterative three-color
DFS) and starts no goroutines when the graph is cyclic, so it cannot wait
forever on a cycle.

## Run the example

```sh
go run ./cmd/demo
```

## Run tests with the race detector

```sh
go test -race -count=1 -v ./...
```

Tests inject task bodies, timing (via channels, no sleeps for synchronization),
and failures; every `Run` is wrapped in a timeout so a scheduling regression
fails fast instead of hanging.
