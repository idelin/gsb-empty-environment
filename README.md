# segtree

Fully persistent segment tree for Go with lazy propagation over `Z/modZ`
(the modulus is chosen by the caller and need not be prime).

## Operations

All updates create a new version in `O(log n)`; old versions stay intact.

- `Assign(ver, l, r, v)` — set `a[i] = v` on `[l, r)`
- `Affine(ver, l, r, mul, add)` — apply `a[i] = mul*a[i] + add` on `[l, r)`
- `Sum(ver, l, r)` — range sum mod `mod`

Semantics:

- Lazy tags are affine transforms composed in application order
  (non-commutative: `Assign` discards older tags, later transforms stack
  on top of it).
- Zero-length ranges are legal: updates are no-ops that still fork a
  version, and sums are `0`.
- All values, including negative ones, are normalized into `[0, mod)`.
- `1 <= mod <= 3037000499` so residue products never overflow `int64`.

## Example

```go
t := segtree.New(5, 7)      // version 0: [0 0 0 0 0] (mod 7)
v1 := t.Assign(0, 0, 5, 3)  // version 1: [3 3 3 3 3]
v2 := t.Affine(v1, 1, 4, 2, 1) // version 2: [3 0 0 0 3]
v3 := t.Affine(v1, 0, 5, 0, 0) // version 3: all zero
fmt.Println(t.Sum(v2, 0, 5))   // 6
fmt.Println(t.Sum(v1, 0, 2))   // 6 — version 1 unchanged
```

## Tests

`go test ./...` cross-checks every version against a naive per-version
array model, including assign-then-multiply-by-zero, negative argument
normalization, interleaved updates on forked versions, zero-length ranges,
`mod = 1`, and `n = 0`.
