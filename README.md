# wavelet — integer wavelet tree (Go, stdlib only)

A real wavelet tree over a coordinate-compressed alphabet. No hash maps in
the index; the only table is the sorted distinct-value array used to map
compressed ids back to original values (negatives round-trip exactly).

## Physical layout

- Balanced binary tree over compressed ids `[0, sigma)`.
- Each internal node: bitvector packed into `[]uint64` (bit i = 1 if the
  i-th element at this node goes right) plus a rank index `[]uint32` with
  one cumulative popcount per 64-bit word. Rank/select walk gives
  O(log sigma) queries; per level exactly n bits are stored.
- `uniq []int`: sorted distinct original values, `id -> value`.

`Tree.Stats()` reports the real byte footprint of each component.

## API

- `New(vals []int) (*Tree, error)` — build; errors on empty input.
- `Access(i) (int, error)` — original value at position i.
- `Quantile(l, r, k) (int, error)` — k-th smallest (1-based) in positions
  `[l, r]`; duplicates count with multiplicity; empty interval (`l > r`)
  returns `ErrEmpty`, out-of-range k returns `ErrBounds`.
- `RangeFreq(l, r, a, b) (int, error)` — count of values in `[a, b]` over
  positions `[l, r]`.
- `Stats() Stats` — real byte occupancy of the index.

Query ranges are never sorted; all queries walk the tree with rank.

## Run

    go test ./...        # tests, incl. randomized cross-check vs brute force
    go run ./cmd/wtdemo  # layout, byte footprint, sample queries
