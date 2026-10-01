# bitpack

Per-block bit-packed integer compression for `int64` slices, Go standard
library only. Real byte-level format — no `map` wrappers.

## Format

```
Header (29 bytes): magic "BPK1" | version u8 | blockSize u32 | totalCount u64
                   | blockCount u32 | indexOffset u64
Block record:      payloadLen u32 | payload | crc32 IEEE u32
Block payload:     flags u8 | bitWidth u8 (0..64) | count u32 (explicit, tail
                   may be short) | base uvarint(zigzag(min)) | excCount uvarint
                   | positions (bitmap ceil(count/8) B or delta uvarints,
                   cheaper one wins) | excValues uvarint each (full deltas)
                   | packed deltas ceil(count*bitWidth/8) B, LSB-first
Index:             per block {offset u64, length u32} = 12 B each
```

- Deltas are `uint64(value) - uint64(base)`; full int64 range including
  `MinInt64..MaxInt64` spans, no shift overflow (zigzag on base, width-64
  capable bit I/O).
- Exceptions (deltas wider than the chosen width) are stored out-of-band as
  full uvarints — a single huge value never truncates and never inflates the
  block width.
- Width 0 is legal: constant blocks (e.g. all zeros) store no packed data.
- The optimizer evaluates every candidate width (0, 64, each distinct delta
  bit-length) with an exact byte cost covering flags, width, count, base,
  exception count, position encoding (bitmap vs delta-varint), exception
  values and packed payload, and picks the minimum. Header, per-record
  length+CRC, and index bytes are all included in `Reader.Size()`.
- Per-block CRC32 gives corruption isolation: a tampered block fails alone,
  other blocks still decode (`DecodeRange` reports failed block indices).
- Decoding validates the declared count against the actual payload size
  before allocating — a corrupt pseudo length yields an error, not an
  allocation.

## Usage

```go
data := bitpack.Encode(vals, 128)   // compress
r, _ := bitpack.Open(data)          // parse header + index
v, _ := r.At(511)                   // random access, decodes one block
vals, failed := r.DecodeRange(0, r.Len())
all, err := r.DecodeAll()           // errors if any block is corrupt
layout, _ := r.Layout()             // real per-block layout and byte sizes
```

Run the demo: `go run ./cmd/demo` — prints the encoded layout, exact byte
accounting, random access, range decode and a tamper-isolation demo.

Tests: `go test ./...`
