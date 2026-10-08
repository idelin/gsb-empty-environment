# quant

Exact solvers for small image quantization problems in Go.

## Single-image quantization (`quantize.go`)

`Quantize(levels, weights, k)` picks `k` integer representatives for at
most twelve distinct integer gray levels, minimizing the weighted squared
error. Duplicate gray values are merged, zero weights are allowed, and
`k` larger than the number of distinct levels is clamped.

- Optimal 1-D clusters are contiguous, so the result is a partition of
  the sorted levels into contiguous `Group`s plus a `Rep` mapping from
  every gray level to its representative.
- Representatives are integers; when floor/ceil of the real centroid tie,
  the smaller value wins. Zero-weight groups take their smallest level.
- When several partitions are equally optimal, the lexicographically
  smallest tuple of group start indices is returned.
- Costs are exact (`math/big`); the DP cost is cross-checked in tests
  against partition enumeration with a full integer centroid search.

## Multi-frame quantization (`frames.go`)

`SolveFrames(frames, palette)` quantizes a sequence of frames that share
one fixed palette. Frame `t` may change the color assignment of at most
`frames[t].Budget` pixels relative to frame `t-1`, and its squared error
must stay under `frames[t].MaxErr` (negative means no cap). The total
error over all frames is minimized; the result carries the per-frame
mapping, per-frame error, and the migration budget actually used.

`OptimizeFrames(frames, domain, k)` additionally searches every
`k`-element palette drawn from the finite gray domain (defaults to the
values occurring in the frames) and returns the best, breaking ties
toward the lexicographically smallest palette.

Both solvers are exact dynamic programs over assignment vectors, so they
target small images and small palettes. Tests verify them against full
enumeration of all assignment sequences, and include a case proving the
frames cannot be quantized independently: the independent optimum
violates the migration budget and the constrained joint optimum is
strictly worse than the unconstrained one.

## Tests

```sh
go test ./...
```
