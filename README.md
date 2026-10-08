# graphcontract

Go multigraph library with partition-based contraction.

## Features

- **Multigraph**: parallel edges and self-loops are first-class (`Graph`, `AddEdge`).
- **Partition contraction**: `Contract(g, p, opts)` collapses each group of a
  `Partition` into one node. The partition must cover exactly the graph's
  nodes; empty groups, duplicate nodes, missing or unknown nodes are rejected.
- **Parallel edges preserved**: by default every cross-group edge keeps its
  own contracted edge; `MergeParallel` merges edges between the same
  contracted endpoints into one.
- **Provenance**: the returned `Mapping` expands every contracted node/edge
  back to the original nodes/edges (`Members`, `Sources`, `DroppedEdges`).
- **Self-loops**: in-group edges are kept as self-loops or dropped (and
  recorded) depending on `KeepSelfLoops`.
- **Attribute aggregation**: merged edges combine attributes per key with an
  `Aggregator` (default `SumAttributes`).
- **Composition**: contracting twice and composing the mappings with
  `Mapping.Then` yields the same provenance as one contraction with
  `ComposePartitions(p1, p2)`.

## Example

```go
g := graphcontract.New(true)
g.AddEdge(0, 1, graphcontract.Attributes{"w": 1})
g.AddEdge(0, 1, graphcontract.Attributes{"w": 2}) // parallel
g.AddEdge(1, 2, graphcontract.Attributes{"w": 3})

p, _ := graphcontract.NewPartition([][]graphcontract.NodeID{{0, 1}, {2}})
cg, m, _ := graphcontract.Contract(g, p, graphcontract.ContractOptions{
    MergeParallel: true,
})
// cg has 2 nodes; the merged self-loop on node 0 has w=3 and
// m.Sources(e) == [0 1]; m.Members(0) == [0 1].
```

Run tests with `go test ./...`.
