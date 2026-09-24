# bitplane

Bit-plane compression for fixed-width integers in pure Go (standard library only).

- Splits values into bit planes; each plane uses the cheapest of **literal**,
  **run-length**, or **delta** encoding, priced including its record header.
- Optional **signed (zigzag) mapping**; decoding sign-extends back so negative
  values keep their exact bit pattern.
- Block size and modes are chosen to minimize the **real total byte count**,
  including the stream header, the block index, and per-plane record headers.
- A front **block index** (length + CRC32 per block) enables **random range
  decoding** without touching unrelated blocks; corruption stays local.
- Explicit **version, lengths, and checksums** (header CRC, per-block CRC,
  whole-stream trailer CRC). Parsing validates version and bit width first,
  and decoding enforces a bounded **output quota**.
- All-zero planes collapse to an empty payload; a short tail block is always
  stored.

## Format

```
header:  "BPC1" | version u8 | flags u8 (bit0: signed) | bits u8 | reserved u8
         | count uvarint | blockSize uvarint | numBlocks uvarint | headerCRC u32
index:   per block: payloadLen uvarint | payloadCRC u32
blocks:  per block, per plane: mode u8 | payloadLen uvarint | payload
trailer: dataCRC u32 (CRC32 of decoded int64 values, 8-byte LE each)
```

## Usage

```go
data, err := bitplane.EncodeBytes(vals, bitplane.Options{Bits: 32, Signed: true})
all, hdr, err := bitplane.DecodeAll(data, maxValues)
part, _, err := bitplane.DecodeRange(data, lo, hi, maxValues)
```

## Run

```
go test ./...    # tests, including exhaustive optimality on small inputs
go run ./cmd/bpc # demo: encode 200k signed values, range-decode a slice
```
