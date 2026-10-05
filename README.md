# dagpath

Best-path solver for a DAG whose edges carry string fragments (empty
fragments allowed). From `s` to `t` it minimizes, in order:

1. the concatenated label string, compared **as a whole** — never per edge;
2. the number of edges;
3. the edge-ID sequence, lexicographically.

## Usage

```go
g, err := dagpath.NewGraph(3, []dagpath.Edge{
    {ID: 0, From: 0, To: 1, Label: "a"},
    {ID: 1, From: 1, To: 2, Label: "z"},
    {ID: 2, From: 0, To: 2, Label: "ab"},
})
path, found, err := g.BestPath(0, 2)
// path.String()  == "ab"   (whole-string compare beats per-edge greedy "a"+"z")
// path.EdgeIDs() == [2]
```

`Path` is a plain `[]Edge` slice — fully independent of solver internals, so
it can be expanded (`String`, `EdgeIDs`) anywhere.

## Design

- **Reverse-topological DP.** `best[u]` = best way to reach `t` from `u`.
  Optimal substructure holds because the objective is a lexicographic product
  and prepending one label preserves string order. Each edge is relaxed once.
- **Shared suffixes.** Candidate strings are immutable concat ropes
  (`rope.go`); `best[v]`'s rope is shared by every predecessor of `v`, so big
  suffixes are never re-concatenated or copied.
- **Whole-string compare without flattening.** Two ropes are compared by
  binary-searching their longest common prefix with a dual 64-bit rolling
  hash, then inspecting one byte — O(depth · log n) instead of O(n). Strict
  prefixes compare smaller, matching `strings.Compare`.
- **Verification.** `solve_test.go` cross-checks the solver against full
  enumeration of all paths on random small DAGs (including empty fragments
  and prefix-related labels); `rope_test.go` cross-checks the rope comparator
  against `strings.Compare`, with explicit prefix-relation cases.

## Test

```sh
go test ./...
```
