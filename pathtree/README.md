# pathtree

Go library maintaining integer loads on tree nodes.

## Operations

- `AddPath(u, v, delta)` — add `delta` (may be negative) to every node on
  the undirected path `u`–`v`, endpoints included. `O(log² n)`.
- `QueryPath(u, v)` — returns the maximum load on the path and the
  smallest-numbered node attaining it. `O(log² n)`.
- `SetRoot(r)` — changes the logical root. Paths are undirected, so results
  of `AddPath`/`QueryPath` are identical for every root choice.

## Design

- Heavy-light decomposition over a fixed decomposition root, with a
  lazy-propagation segment tree on the heavy-path order. Each segment keeps
  `(max, minNode)`; ties resolve to the smallest node number.
- All tree traversals are iterative (explicit stacks); the only recursion is
  inside the segment tree, whose depth is `O(log n)`. Long chains therefore
  never depend on the call stack — verified by `TestLongChainNoRecursionStack`
  on a chain of 1,048,576 nodes.

## Usage

```go
tr := pathtree.NewWithValues([]int64{3, -1, 4})
tr.AddEdge(0, 1)
tr.AddEdge(1, 2)
tr.Build()

tr.AddPath(0, 2, -2)            // negative deltas allowed
max, node := tr.QueryPath(0, 2) // max = 2, node = 2
tr.SetRoot(2)                   // does not affect path results
```

## Tests

`go test ./...` runs:

- `TestRandomAgainstBrute` — random trees and mixed operations (adds with
  negative deltas, queries, re-roots) checked against point-by-point BFS
  traversal of the path.
- `TestSingleNode` — the one-node tree.
- `TestNegativeDeltas`, `TestTieBreakMinNode` — negative maxima and
  smallest-node tie-breaking.
- `TestLongChainNoRecursionStack` — a 2^20-node chain, proving no recursion
  proportional to tree depth.
