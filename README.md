# roadgraph

Go library for shortest-path queries on directed multigraphs with
non-negative integer weights, plus a versioned/time-bounded variant with a
dependency-aware path cache.

## Replacement paths (`replacement.go`)

`ReplacementPaths(g, source, target, path)` takes a specified shortest path
(a chain of edge IDs) and returns, for every edge on it, the shortest
source-target distance with that single edge removed, plus one witness path.
Each removal is checked with a fresh Dijkstra run (per-edge Dijkstra).

- Parallel edges are distinguished by unique `EdgeID`; removing one never
  affects its siblings.
- Unreachable results are marked explicitly: `Reachable=false`,
  `Distance=-1`, `Path=nil`.
- Zero-weight cycles and multiple equal-length shortest paths are handled
  (see tests, including a brute-force simple-path oracle cross-check).

## Versioned graph (`versioned.go`)

Edges carry revisions (`Revision{Rev, Start, End, Cost, Closed}`): costs and
closure events, each valid over `[Start, End)` and tagged with a revision
number. A `Query{Knowledge, Depart, Source, Target}` fixes a knowledge
version and a departure time; `Freeze` materializes one snapshot where an
edge is present iff its latest revision with `Rev <= Knowledge` covers
`Depart` and is not a closure. Every edge of a path is judged against this
single frozen graph. `VersionedGraph.Query` answers by full recompute and
serves as the ground-truth oracle.

## Path cache (`cache.go`)

`PathCache` memoizes query results and invalidates them when local revisions
arrive. Shortestness of a cached path does **not** depend only on the edges
of that path — a revision lowering an external edge's cost can dethrone it —
so each entry records its full dependency set: every edge examined while
freezing the graph and running Dijkstra. An entry for knowledge version `K`
is dropped when a revision is visible at `K` (`Rev <= K`) and either touches
the dependency set or backfills an edge previously unknown at `K`.
`TestCacheExternalEdgePriceDrop` and `TestPathOnlyInvalidationIsUnsound`
demonstrate the counterexample that path-only invalidation gets wrong, and
`TestCacheMatchesFullRecomputeUnderRevisionStream` checks the cache against
full recompute under a random revision stream.

## Tests

```sh
go test ./...
```
