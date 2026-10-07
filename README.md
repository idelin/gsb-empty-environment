# bitmap

A compressed 32-bit bitmap for Go, in the spirit of Roaring Bitmaps.

## Design

- Values are chunked by their high 16 bits; each chunk is a *container*.
- Sparse chunks use a sorted, deduplicated `[]uint16`; dense chunks use a
  65536-bit bitmap with an incrementally maintained cardinality cache.
- The sparse/dense threshold is configurable via `WithArrayMaxSize`
  (default 4096). Every mutating operation (`Add`, `Remove`, `And`, `Or`,
  `AndNot`, `Flip`) restores the canonical form: arrays hold at most
  `threshold` values, bitmaps hold more, and empty chunks are dropped.
- The bitmap-level cardinality is cached and invalidated on mutation.

## Serialization

`MarshalBinary` / `UnmarshalBinary` use a self-describing format:
magic `"RBMP"`, a version uint16, the writer's threshold, the container
stream, and a trailing CRC32-IEEE checksum. On decode the checksum, magic,
and version are verified; stored cardinality hints are never trusted —
cardinalities are recomputed from payloads and containers are
re-normalized, so corrupt or hand-crafted files cannot smuggle in
non-canonical state.

## Testing

`bitmap_test.go` cross-checks every operation against a plain
`map[uint32]bool` model with a tiny threshold to force containers across
the array/bitmap boundary in both directions, verifies canonical form and
cache accuracy after each step, and confirms that truncated, re-versioned,
bit-flipped, or otherwise corrupted files are rejected.

```sh
go test ./...
```
