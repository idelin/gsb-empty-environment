# onion

Convex-hull layer decomposition (onion decomposition) for integer point sets, in Go.

`Layers` repeatedly peels the convex-hull boundary: layer 0 is the boundary of the
convex hull of the whole set, layer 1 the boundary of what remains, and so on.

```go
layerOf, rings := onion.Layers(pts)
```

- `layerOf[i]` — 0-based layer of input point `i` (0 = outermost).
- `rings[k]` — canonical boundary ring of layer `k` as input indices:
  - non-degenerate layers: counterclockwise, starting at the smallest `(X, Y, index)`;
  - degenerate layers (single point / all remaining points collinear): sorted
    ascending by `(X, Y, index)`.

Guarantees:

- Points collinear with a hull edge belong to the same layer as its endpoints.
- Duplicate coordinates always share a layer; every input index keeps its identity
  and appears in exactly one ring.
- `Orient` and `Area2` use exact big-integer arithmetic, so orientation and area
  never flip due to overflow, even with coordinates near the int64 limits.

## Testing

The test suite cross-checks the decomposition against an independent supporting
half-plane characterization of the hull boundary, applied recursively per layer:

- exhaustive enumeration of all 511 non-empty subsets of a 3x3 grid;
- exhaustive multisets (with duplicates) of sizes 1-5 over a 2x2 grid;
- all-collinear sets, single point, empty set, fully duplicated sets;
- extreme coordinates (`±2^62`) where int64 arithmetic would overflow;
- randomized small and full-range-int64 point sets.

Run with:

```sh
go test ./...
```
