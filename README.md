# matching

Go library for bipartite (assignment) graphs that may not have a perfect
matching. It computes a maximum-cardinality matching and identifies the
**essential edges**: the vertex pairs that appear in every maximum matching.

## Semantics

- Input edges may be duplicated; each duplicate gets its own edge id, but
  all computations deduplicate by `(Left, Right)` vertex pair.
- `MaxMatching` and `EssentialEdges` report the canonical (smallest) edge
  id of each matched/essential pair.

## Algorithm

- Maximum matching: Hopcroft–Karp, `O(E sqrt(V))`.
- Essential edges: orient matched edges right→left and unmatched edges
  left→right. A matched pair `(u, v)` is essential iff `u` and `v` are in
  different strongly connected components (no alternating cycle through the
  edge), `v` is not reachable from any free left vertex, and `u` cannot
  reach any free right vertex (no even alternating path that flips the
  edge out). `O(E)` with Kosaraju SCC + two BFS sweeps.

## Verification

`matching_test.go` enumerates **all** matchings by brute force on random
graphs up to 8x8 (including duplicate edges) and cross-checks both the
maximum matching size and the exact set of essential pairs. Targeted tests
cover alternating cycles, unmatched vertices, free-vertex alternating
paths, duplicate edges, and empty graphs.

```sh
GO111MODULE=on go test ./...
```
