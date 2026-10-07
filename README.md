# pseq — persistent sequence with polynomial digests

Immutable persistent integer sequence for Go (stdlib only, Go 1.11+).

## Model

- Backed by an immutable AVL rope: `PushBack` / `PushFront` / `PopBack` /
  `PopFront` / `Concat` / `Slice` are all O(log n) and return new versions;
  receivers are never mutated, so versions share underlying nodes safely —
  concatenating two versions that share nodes cannot produce mutable
  aliasing.
- Every version carries a **polynomial digest** under the factory modulus
  (`NewFactory(mod)`, `2 <= mod <= 2^32-1`, default `1e9+7`) plus its
  length. Negative elements are normalized into `[0, mod)` before hashing.
  Different operation histories reaching the same sequence yield the same
  digest and length.
- A digest is only a summary: equal digests never imply equal content.
  `Equal` compares raw elements structurally, so modular collisions
  (e.g. `-1` vs `mod-1`, or anything under a tiny modulus) are correctly
  reported as unequal.
- `Evidence(lo, hi)` returns the interval digest and length for `[lo, hi)`.
- `Store` holds named snapshots. `Release` drops a root reference; nodes
  reachable only from that snapshot become unreachable and are reclaimed
  by the runtime. `LiveNodes` reports the exact reachable-node count as
  reclamation evidence.

## Tests

`go test ./...` runs model-based checks that recompute everything from a
plain array oracle: zero elements and both-end drains, same-sequence
digest stability across op histories, negative normalization, mod
collisions vs content equality, shared-node concat aliasing, snapshot
release reclamation, forks, repeated self-concat, interval evidence, and
a 4000-step randomized fuzz over all operations.
