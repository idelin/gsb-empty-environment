# erasure

Systematic block erasure coding (Reed-Solomon style, MDS) over GF(256) in
pure Go standard library. No third-party or compression libraries.

## Design

- **Field**: GF(256) with explicit irreducible polynomial
  `p(x) = x^8 + x^4 + x^3 + x^2 + 1` (`0x11d`), generator 2, log/exp tables
  (`gf.go`).
- **Coding matrix**: Vandermonde matrix over evaluation points
  `0..K+M-1`, right-multiplied by the inverse of its top KxK submatrix,
  yielding systematic form (top K rows = identity) with the MDS property:
  any K of the K+M blocks recover the original (`codec.go: buildGenerator`).
- **Decoding**: Gauss-Jordan inversion of the KxK submatrix selected by the
  surviving block IDs (`matrix.go`), then GF(256) recombination.

## Block wire format (16-byte header + payload)

| Offset | Size | Field                        |
|--------|------|------------------------------|
| 0      | 1    | format version (= 1)         |
| 1      | 1    | K (data blocks)              |
| 2      | 1    | M (parity blocks)            |
| 3      | 1    | block ID, in [0, K+M)        |
| 4      | 4    | original length, big-endian  |
| 8      | 4    | shard length, big-endian     |
| 12     | 4    | CRC-32 (IEEE) of payload     |
| 16     | ...  | payload (shard length bytes) |

## Decode rules

- Any subset of >= K valid blocks recovers the original; fewer returns
  `ErrInsufficient`.
- Checksum-mismatched blocks are rejected and never participate in solving
  the linear system.
- Duplicate block IDs return `ErrDuplicateID`; bad version, truncation,
  out-of-range ID and inconsistent headers are rejected with typed errors.
- Decoded output is bounded: original length > `outputLimit` returns
  `ErrOutputLimit` before decoding.

## Usage

```go
blocks, _ := erasure.Encode(data, 4, 2)     // 4 data + 2 parity
raw := [][]byte{}
for _, b := range blocks { raw = append(raw, b.Marshal()) }
out, err := erasure.Decode(raw[1:5], 1<<20) // any 4 of 6 blocks
```

## Test & demo

```sh
export GO111MODULE=on
go test ./... -v
go run ./cmd/demo
```
