# compute

A Go library for shared, reference-counted computations over a dependency
graph.

## Model

- `Graph.Add(key, deps, fn)` defines a named computation. Dependencies may
  be forward references; the graph must stay acyclic (`ErrCycle`, rolled
  back).
- `Graph.Subscribe(keys...)` acquires one reference on each key's whole
  subgraph. One query therefore shares all of its underlying
  sub-computations with any other query that overlaps.
- `Subscription.Results(ctx)` blocks for the current generation's values.
- `Subscription.Close()` releases exactly the references this subscription
  acquired. A sub-computation is canceled and its cached result dropped
  only when its reference count reaches zero.

## Semantics

- **Sharing**: each node runs at most one generation at a time. In a
  diamond (`d <- b, c <- a`), `a` is computed once and holds one reference
  per parent edge.
- **Cancellation**: when the last reference goes away mid-flight, the
  generation's context is canceled and its late result is discarded.
- **Failure**: a failed generation propagates its error to every
  still-subscribed waiter (dependents fail with the dependency's error)
  and is never cached — a new subscription starts a new generation.
- **Caching**: a successful generation is cached while referenced; new
  subscribers reuse it without recomputation.

## Verification

`go test -race ./...` covers diamond sharing, partial subscriber exit,
cancel-at-zero-refs with late results, failure propagation and retry,
cycle rejection (self-loop, 2-node, 3-node via forward references), and
concurrent subscribe/close churn.
