# bitmap

A compressed bitmap over `uint32` values, in the spirit of Roaring Bitmaps.

- **Chunked by high bits**: values are grouped into containers keyed by the
  high 16 bits.
- **Dual container encoding**: sparse containers are sorted `[]uint16`
  arrays, dense containers are 65536-bit bitmaps. The switch threshold is
  configured per bitmap via `New(arrayMaxSize)`.
- **Canonical representation**: `Add`, `Remove`, `And`, `Or`, `AndNot` and
  `FlipRange` always restore the canonical encoding (array iff cardinality
  <= threshold) and keep the per-container cardinality cache exact.
- **Serialization**: `MarshalBinary`/`Unmarshal` embed a format version and
  a trailing CRC-32 checksum. Stored cardinalities are never trusted on
  load — they are recomputed from the payload and containers are
  re-normalized. Corrupt, truncated, non-canonical or wrong-version data
  is rejected.

## Example

```go
a := bitmap.New(4096)
a.Add(42)
a.FlipRange(1000, 2000)

b := bitmap.Default()
b.Add(42)

c := a.And(b)   // also: Or, AndNot
fmt.Println(c.Cardinality())

data, _ := a.MarshalBinary()
back, err := bitmap.Unmarshal(data)
```

## Tests

`go test ./...` runs a randomized differential test against a plain
`map[uint32]bool` model (with small random thresholds to force constant
array/bitmap crossings), threshold-crossing unit tests, and corrupted /
non-canonical file rejection tests.
