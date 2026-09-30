# fpcodec

Bit-exact binary encoding and correctly rounded format conversion for
IEEE-754-style floats (16/32/64-bit), in pure Go with only the standard
library.

## Design

- **Storage and text are separate.** The wire format stores raw bit
  patterns; `Text` renders a display form and is never used for storage.
- **No host float in the data path.** Encoding, decoding and conversion
  use integer math only, so NaN payloads, signaling bits, subnormals and
  the sign of zero are never canonicalized by hardware.
- **Decoding executes no host code.** It is byte parsing only: no
  `unsafe`, no reflection, no `gob`. Lengths are validated before any
  allocation.

## Wire format (self-defined)

Fixed 10-byte header, then payload:

| Offset | Size | Meaning |
|--------|------|---------|
| 0 | 2 | magic `F` `P` |
| 2 | 1 | version (1) |
| 3 | 1 | width: 16, 32 or 64 (others rejected) |
| 4 | 1 | payload endianness: 0 = little, 1 = big |
| 5 | 1 | reserved, must be 0 |
| 6 | 4 | element count, uint32 big-endian, <= `MaxElements` (2^24) |

Payload: `count` elements of `width/8` bytes each, in the declared
endianness. Supported serialization range: element widths 16/32/64, up to
`MaxElements` elements per stream. `Unmarshal` requires the buffer length
to match the header exactly; `Decode` streams from any `io.Reader` and
tolerates arbitrary chunking.

## Conversion

`Convert(bits, from, to, policy)` converts between widths with
round-to-nearest-ties-to-even and returns exception flags
(`FlagInexact`, `FlagUnderflow`, `FlagOverflow`, `FlagInvalid`).

Explicit policies (`DefaultPolicy` shown):

- **NaN payload**: `NaNTruncateMSB` — narrowing keeps the most
  significant payload bits; widening left-shifts. A payload that would
  truncate to zero is forced to 1 so a NaN never becomes an Inf.
- **Signaling NaN**: `SignalingQuiet` — the quiet bit is set and
  `FlagInvalid` is raised.
- **Subnormals**: `SubnormalPreserve` (exact IEEE behavior) or
  `SubnormalFlushToZero` (inputs silently treated as signed zero;
  would-be subnormal results flushed with `FlagUnderflow|FlagInexact`).

Bit-preserving transport (encode/decode) is a separate concern from
numeric conversion: the codec never rounds, `Convert` never serializes.

## Comparison

Compare floats by bit pattern (`BitsEqual`, `AllBitsEqual`), never with
host `==`: NaN != NaN and +0 == -0 under IEEE equality, both of which
would corrupt round-trip checks.

## Verification

- All 65536 16-bit patterns: converted to 32/64-bit and checked against
  an independent `math/big` exact-integer reference, then round-tripped
  back bit-exactly.
- 32→16 rounding: every adjacent float16 pair (both signs) is checked at
  its exact float32 midpoint (ties-to-even) and one ulp to either side.
- Random cross-checks across all width pairs against the reference.
- Codec: both endiannesses, endianness switching, arbitrarily chunked
  readers, forged/truncated headers, unsupported widths.

## Run

```sh
go test ./...
go run ./cmd/fpdemo
```
