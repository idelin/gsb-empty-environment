# quant

Exact solvers for integer gray-level palette quantization, written in Go
(no dependencies, Go 1.11+).

## Part 1 — Optimal integer palette (`palette.go`)

`Quantize(levels, k)` chooses `k` integer representatives for at most
twelve (any number of) weighted integer gray levels, minimizing the total
weighted squared error.

- Duplicate colors are merged (weights summed); zero-weight levels cost
  nothing but still receive a representative.
- The optimum always partitions the sorted distinct values into `k`
  contiguous groups, so every composition of cuts is enumerated and each
  candidate group's best integer centroid is found exactly: the weighted
  mean lies in `[floor(μ), floor(μ)+1]`, and by convexity one of the two
  is optimal.
- Deterministic tie-breaking: centroid ties take the smaller integer; a
  zero-weight group uses its smallest value; equally optimal partitions
  take the lexicographically smallest cut tuple.
- Output: contiguous `Groups` (Lo/Hi/Rep/Weight/Cost), the ordered
  `Palette`, the gray→representative `Rep` map, and the total `Cost`.
- `k` larger than the number of distinct values is clamped.

`TestQuantizeMatchesBruteForce` cross-checks 300 randomized instances
(including duplicates and empty weights) against an independent brute
force that tries every integer representative in every group of every
partition.

## Part 2 — Multi-frame quantization with a migration budget (`frames.go`)

`QuantizeFrames(frames, palette, budget, maxFrameErr)` quantizes a
sequence of same-sized frames that share one fixed palette:

- **Migration budget**: for each `f ≥ 1`, at most `budget[f-1]` pixels
  may change their palette index relative to frame `f-1`.
- **Frame error limit**: every frame's squared error must be
  `≤ maxFrameErr` (negative means unlimited); infeasibility is reported
  as an error.
- **Objective**: minimize the total squared error over all frames.

The exact optimum is found by dynamic programming over all
`paletteSize^N` per-frame assignment vectors (a shortest path over
frame layers with Hamming-distance-constrained edges), suitable for
small images and finite gray domains; instances beyond `maxStates`
(4096) are rejected. Ties resolve to the lexicographically smallest
assignment sequence. Output: per-frame `Assign` mappings, per-transition
`Changed` migration counts, per-frame `FrameErr`, and the `Total`.

`CombinedHistogram(frames)` + `Quantize` derive a shared palette from
all frames at once.

`TestFramesMatchesBruteForce` cross-checks 200 randomized instances
against full enumeration of all assignment sequences, and
`TestFramesCannotQuantizeIndependently` shows a case where quantizing
each frame independently is cheaper but violates the migration budget,
so the frames must be optimized jointly.

## Usage

```go
levels := []quant.Level{{Value: 0, Weight: 5}, {Value: 10, Weight: 5}}
pal, err := quant.Quantize(levels, 2)

frames := [][]int{{1, 9}, {9, 1}}
res, err := quant.QuantizeFrames(frames, pal.Palette, []int{1}, -1)
```

## Test

```sh
GO111MODULE=on go test ./...
```
