# taskgraph

A Go task-graph runner that memoizes task construction by canonical
parameter keys.

- `Runner.Do(ctx, key, build)` constructs each key at most once at a
  time: concurrent callers share one future, and successful results are
  memoized for later independent requests.
- A dependency that points back at an ancestor still under construction
  on the same call chain returns a `*CycleError` (with the full key
  path) immediately, instead of deadlocking on its own future.
- Failed constructions are evicted, not memoized, so the next request
  retries the constructor.
- `Key(name, params...)` derives collision-free dedup keys: every
  segment is length-prefixed and parameters are JSON-encoded, so
  distinct parameter tuples can never blur into the same key.

Run the tests (diamond sharing, retry after failure, cycle detection,
encoding collisions) with:

    go test -race ./...
