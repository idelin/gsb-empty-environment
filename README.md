# dominator

Go library for dominance analysis on control-flow graphs with a single
designated entry node.

## Features

- **Dominance queries** — `Analysis.Dominates(x, y)` answers whether
  every path from the entry to `y` passes through `x`.
- **Dominator tree** — `Analysis.DominatorTree()` returns the child
  map; `Analysis.ImmediateDominator(n)` returns a node's parent.
- **Unreachable nodes** — reported separately via
  `Analysis.Unreachable()`; they never participate in dominance.
- **Duplicate edges ignored** — `Graph` stores adjacency sets;
  inserting an existing edge is a no-op.
- **Incremental updates** — mutate the graph (`AddEdge`/`RemoveEdge`),
  then call `Analysis.Recompute()` to get the affected region: nodes
  whose immediate dominator or reachability changed.
- **Path evidence** — `Analysis.DominancePath(from, to)` produces the
  dominator chain and `Analysis.VerifyDominancePath(path)` validates
  that every step is an immediate-dominator edge.
- **Independent checker** — `DominatesByRemoval(g, a, b)` verifies a
  dominance claim from first principles by deleting the candidate
  dominator and re-testing reachability. The test suite cross-checks
  the dataflow analysis against it on cycles, self-loops, multi-entry
  simulations (a synthetic super-entry fanning out to several entry
  blocks), and randomized graphs.

## Example

```go
g := dom.NewGraph(0)          // entry = 0
g.AddEdge(0, 1)
g.AddEdge(0, 2)
g.AddEdge(1, 3)
g.AddEdge(2, 3)

a := dom.NewAnalysis(g)
a.Dominates(0, 3)             // true
a.Dominates(1, 3)             // false
a.DominatorTree()             // map[0:[1 2 3]]

g.AddEdge(0, 3)               // update an edge...
a.Recompute()                 // ... -> affected region [3]
```

## Test

```sh
go test ./...
```
