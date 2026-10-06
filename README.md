# dynconn

Offline dynamic connectivity for Go. Feed a full history of edge
additions/removals, then ask whether two vertices were connected after
any historical operation index.

Semantics:

- Adding the same edge twice counts two references; the edge
  disconnects only when the last reference is removed.
- Parallel edges and self-loops are supported.
- `Query.Time` may be any value in `[0, len(ops)]`.

How it works: each edge reference is expanded into a lifetime interval
`[add, remove)`; intervals are placed on a segment tree over time and
the tree is walked with a rollback DSU, so state never leaks into
sibling time segments. `SolveBruteForce` rebuilds the graph at every
queried time and serves as the cross-check reference.

```go
ops := []dynconn.Op{
    {Kind: dynconn.AddEdge, U: 0, V: 1},    // op 1
    {Kind: dynconn.AddEdge, U: 1, V: 2},    // op 2
    {Kind: dynconn.RemoveEdge, U: 0, V: 1}, // op 3
}
ans, err := dynconn.Solve(3, ops, []dynconn.Query{
    {Time: 2, U: 0, V: 2}, // true
    {Time: 3, U: 0, V: 2}, // false
})
```

Run tests: `go test ./...`
