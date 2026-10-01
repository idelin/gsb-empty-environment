# cwi — compressed, versioned weight index

Non-negative `uint64` weight sequence with prefix-sum queries, select by
cumulative weight, and atomic batch range increments. Standard library only.

## Encoding layout (real bytes, no map wrapper)

Weights are split into blocks of 64. Each block is stored as:

```
block = uvarint(blockTotal) + 64 x uvarint(delta of local prefix sums)
```

Since weights are non-negative, local prefix sums are monotone and their
deltas are the weights themselves; small weights cost 1 byte. The
block-total layer lets prefix/select skip whole blocks without decoding
them — only overlapping blocks are ever decoded or re-encoded.

## Semantics

- `PrefixSum(v, i)` = w[0]+...+w[i-1], with overflow-checked accumulation.
- `Select(v, t)` = smallest i with `PrefixSum(i+1) > t`. Zero-weight
  elements own an empty interval and are never selected; `t >= total`
  (including `t == total`) returns `n` (past-the-end).
- `RangeAdd(lo, hi, delta)` validates the whole batch (no negative weights,
  no uint64 overflow at element, block, or total level) and commits
  atomically as a new version. Blocks are immutable and shared, so old
  versions stay queryable; only overlapping blocks are re-encoded and the
  report includes the re-encoded block range and bytes written.
- Queries report how many compressed blocks were decoded.

## Run

```
go test ./...
go run ./cmd/demo
```
