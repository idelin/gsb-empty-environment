# graphx

A small Go graph library with parallel edges, edge attributes, and
partition-based contraction that preserves full provenance back to the
source graph.

## Graph

`Graph` holds integer node IDs and a multiset of edges (parallel edges and
self-loops allowed). Each edge gets a stable, sequential ID and an
`Attrs` attribute map.

```go
g := graphx.New(true) // directed
g.AddNode(1)
g.AddNode(2)
g.AddNode(3)
g.AddEdge(1, 3, graphx.Attrs{"w": 1.5})
g.AddEdge(2, 3, graphx.Attrs{"w": 2.5})
```

## Contraction

`Contract` collapses each group of a `Partition` into one node:

```go
c, err := graphx.Contract(g, graphx.Partition{{1, 2}, {3}}, graphx.Options{
    KeepSelfLoops: false,
})
```

- Cross-group edges are kept one-to-one, so parallel edges survive; each
  contracted edge records its source edge IDs in `c.EdgeSources`.
- Intra-group edges become self-loops or are dropped (`EdgeMap[e] == -1`)
  depending on `Options.KeepSelfLoops`.
- Partitions must cover every node exactly once with no empty groups;
  violations return a `*PartitionError` carrying a sentinel reason such as
  `ErrEmptyGroup`.
- `c.Verify()` re-checks the result edge by edge against the source graph.

## Composition law

Contracting twice and expanding the provenance equals contracting once by
the composed partition:

```go
c1, _ := graphx.Contract(g, inner, opts)
c2, _ := graphx.Contract(c1.Graph, outer, opts)
expanded, _ := c2.Expand(c1)          // provenance in terms of g

composed, _ := graphx.Compose(inner, outer)
direct, _ := graphx.Contract(g, composed, opts)
// expanded and direct have identical NodeMap/Groups/EdgeMap/EdgeSources.
```

## Attribute aggregation

`Aggregate` merges parallel contracted edges into one edge per endpoint
pair, combining the attributes of all underlying source edges:

```go
agg := c.Aggregate(graphx.SumFloat("w"))
// agg.Graph has one edge per pair; agg.EdgeSources lists every original
// edge folded into each merged edge.
```

## Tests

```sh
go test ./...
```

Covers empty-group/invalid-partition rejection, parallel-edge provenance,
self-loop handling, the composition law (deterministic and seeded-random
graphs, directed and undirected, both self-loop modes), and aggregation
checked edge by edge against the original graph.
