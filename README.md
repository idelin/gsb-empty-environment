# coreindex

Go library and CLI maintaining **core numbers** (k-core decomposition) of an
undirected simple graph under edge insertions and deletions.

## Guarantees

- Stable node IDs; nodes are never renumbered or removed (isolated nodes are
  kept and reported with core 0).
- Duplicate edge insertions are idempotent; deleting a missing edge is a no-op;
  self-loops are rejected with `ErrSelfLoop`.
- Batched updates are atomic: if any op in the batch is invalid, every change
  (including ensured isolated nodes) is rolled back.
- Updates are **incremental** (Sariyuce et al., *Incremental K-core
  Decomposition*): an edge op with `K = min(core(u), core(v))` only visits the
  connected component of an endpoint inside the subgraph of nodes with
  `core == K`, peeling by "core degree" (neighbors with core ≥ K). Insertions
  move survivors to `K+1`, deletions move peeled nodes to `K−1`. The full-graph
  decomposition is used only for the initial build and as a test reference.
- `ApplyBatch` returns exactly the set of nodes whose core value changed.

## Library

```go
ix := coreindex.New()
d := ix.Build()                       // full decomposition: Core, Order, RemDeg
changed, err := ix.ApplyBatch([]coreindex.EdgeOp{
    coreindex.AddOp(1, 2),
    coreindex.DelOp(3, 4),
}, 99)                                // 99: ensure isolated node
```

`Decomposition.Order` is the deterministic peeling order (increasing residual
degree, ties by ascending node ID); `RemDeg[i]` is the residual degree of
`Order[i]` at removal time. `Decomposition.Consistent` re-derives all core
values from `Order`+`RemDeg` alone (running maximum), and tests additionally
verify each `RemDeg[i]` against the induced subgraph `Order[i:]`.

## CLI

```
corecli build  -edges edges.txt [-state state.json]   # cores + order + remdeg
corecli update -state state.json -ops ops.txt [-out newstate.json]
corecli check  -state state.json                      # full-decomposition reference
```

Edge file: `u v` per line, `N u` for isolated nodes, `#` comments.
Ops file: `A u v` / `D u v` / `N u`; one file = one atomic batch.
`update` prints the changed nodes; on batch failure it exits non-zero and
leaves the state file untouched.

## Tests

```
go test ./...
```

- Scenario tests: two 5-cliques joined by a low-core bridge; deleting a key
  internal clique edge cascades (K5: all 4→3, K4: all 3→2); repeated ops;
  isolated nodes; batch rollback.
- `TestKCoreSubsetEnumeration`: on small graphs, enumerates **all** vertex
  subsets and checks that `{v : core(v) ≥ k}` equals the union of all induced
  subgraphs with minimum degree ≥ k (the maximal such union), and equals
  iterative peeling.
- `TestIncrementalVsFullDecomposition`: random batches; after each batch the
  maintained cores and the reported changed set are compared against a
  full-graph decomposition reference.
