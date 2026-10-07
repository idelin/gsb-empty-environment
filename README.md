# graphcanon

Exact canonical encodings for small rooted, directed, node-labeled
multigraphs (at most 8 nodes).

## Model

- Rooted, directed, node-labeled multigraph, `1 <= n <= 8` nodes.
- Parallel edges are first-class: the graph is stored as a multiplicity
  matrix, so **edge insertion order is irrelevant** and **parallel-edge
  counts are preserved** exactly.
- Node indices are **stable identities**: mutations (`AddEdge`,
  `RemoveEdge`, `SetLabel`) never renumber nodes, and every mutation call
  **increments the graph version**.

## Canonicalization (exact, no hashing)

`Canonicalize(g)` enumerates **every** root-fixing numbering
(`(n-1)! <= 7! = 5040` for 8 nodes) and takes the **lexicographically
minimal encoding** (`canon.go`). There is no recursive hashing and no
cycle truncation: each numbering is fully materialized into an injective
encoding (`encode.go`: size header, length-prefixed labels in canonical
order, fixed-width hex adjacency multiplicities).

Therefore:

- isomorphic graphs get the **same** `Code` (same set of numbered
  encodings, same minimum);
- non-isomorphic graphs get **different** `Code`s (injectivity prevents
  collisions).

`Result` also carries:

- `Numbering`: stable id -> canonical position (root is always 0; ties
  among minimal encodings are broken by the lexicographically smallest
  numbering, so the map is deterministic);
- `Orbits`: automorphism orbits in stable node identities;
- `Automorphisms`: the full automorphism group of the canonical form —
  a complete, checkable proof of the symmetry structure.

## Independent verification

`verify.go` shares no logic with the canonicalizer:

- `VerifyPermutation(a, b, perm)` validates a claimed isomorphism
  directly against the structure (bijection, root, labels, exact
  multiplicities);
- `AreIsomorphic(a, b)` decides isomorphism by **exhaustive permutation
  search**, pruned only by locally checkable necessary conditions, with
  every complete candidate validated by `VerifyPermutation`.

The test suite cross-validates the two paths on random graphs for all
`n <= 8`: equal canonical codes iff `AreIsomorphic` agrees, and every
reported automorphism is re-checked by the independent permutation checker.

## Versioned canonicalization service

`Service` (`service.go`) serves canonical forms for one mutable graph:

- results are cached per graph version, and the latest exact result is a
  reuse candidate across version bumps (a local modification may leave
  the structure unchanged);
- a cached result is served **only after its proof is re-validated**
  against the current state: the numbering must still reproduce the code
  (by injectivity this proves the structure unchanged), every claimed
  automorphism is re-verified by `VerifyPermutation` in stable-id space,
  and the orbits must equal the orbits of the re-verified group;
- if any check fails, the service falls back to exact enumeration, which
  is always affordable at this size.

## Usage

```go
g, _ := graphcanon.NewGraph(4, 0, []string{"a", "a", "a", "a"})
g.AddEdge(0, 1)
g.AddEdge(0, 1) // parallel edge: multiplicity 2

svc := graphcanon.NewService(g)
res := svc.Canonical()
_ = res.Code      // canonical encoding
_ = res.Numbering // stable id -> canonical position
_ = res.Orbits    // automorphism orbits in stable ids

g.SetLabel(1, "b") // bumps the version; next Canonical() revalidates
```

## Tests

```
go test ./...
```

Covers permutation invariance, exhaustive isomorphism cross-checks, edge
order independence, parallel-edge counting, orbit/group-size ground
truth, independent automorphism verification, determinism, and service
caching/revalidation behavior (including corrupted-proof detection).
