# treedecomp

Go library for validating a candidate tree decomposition of an undirected
graph.

## API

```go
res := treedecomp.Validate(g treedecomp.Graph, d treedecomp.Decomposition) treedecomp.Result
```

- `Graph{N, Edges}` — simple undirected graph on vertices `0..N-1`.
- `Decomposition{Bags, Edges}` — bags indexed `0..len(Bags)-1` joined by
  tree edges.
- `Result{Valid, Width, Violation}` — `Width` is max bag size minus one
  (a single empty bag gives `-1`); `Violation` is the first concrete
  witness, `nil` iff valid.

## Checks, in order

1. Bag vertices and tree edges are in range; no self-loops or duplicate
   tree edges.
2. The bag graph is a tree: `|E| = |V| - 1` and connected.
3. Every graph vertex appears in at least one bag.
4. Every graph edge is contained in some bag.
5. Running intersection, via the **tree-edge deletion partition test**:
   removing a tree edge `(a,b)` splits the bags into two sides; any vertex
   appearing on both sides must lie in `bag(a) ∩ bag(b)`. This is strictly
   stronger than checking pairwise bag intersections — e.g. bags
   `{0,1},{1,2},{0,2}` in a path intersect pairwise, yet vertex `0`'s bags
   are disconnected, and only the partition test rejects it.

Each violation carries concrete witnesses (offending vertex, uncovered
edge, cut tree edge plus one witness bag on each side, etc.).

The empty graph (`N = 0`) is valid with a single empty bag.

## Test

```sh
go test ./...
```
