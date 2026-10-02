# floatwire

Bit-exact serialization and conversion of IEEE 754 binary16 / binary32 /
binary64 values in pure Go (standard library only).

Values are always handled as raw bit patterns (`uint64`), never through
host floating-point arithmetic, so signed zero, infinities, subnormals and
NaN payloads survive every round trip and conversion.

## Supported serialization scope

- Widths: **16, 32, 64 bits only**. Any other width is rejected up front
  with `ErrUnsupportedWidth` by every entry point.
- Wire form: fixed-width byte sequence, canonical **big-endian**;
  `EncodeOrder`/`DecodeOrder` offer an explicit little-endian option.
  Either order restores bit-identical values.
- Frame form: `[0xF1][width byte][payload]` self-describing records.
  `DecodeFrame` validates the declared width and the available length
  **before** allocating or reading any payload byte; decoding is pure bit
  assembly and never executes host code paths driven by the input.
- Streams: `StreamEncoder`/`StreamDecoder` reassemble fixed-width records
  from arbitrarily chunked input; chunk boundaries never change the
  decoded bits.

## Text display vs. exact storage

`Format` is display-only (`-0`, `Inf`, `NaN(0x…)`, `sNaN(0x…)`, shortest
decimal for finite values). The exact storage form is `Encode`. NaN
payloads are rendered from raw bits, never via host floats.

## Conversion

`ConvertFlags(from, to, bits, mode)` converts between widths with IEEE 754
round-to-nearest-ties-to-even and reports exception flags
(`Invalid`, `Overflow`, `Underflow`, `Inexact`). Each value is rounded
exactly once (no double rounding in the subnormal range).

Two modes, separated contractually:

- `ModeBitPreserving` — guarantees: sign of zero kept; infinities map to
  infinities; NaN payloads propagated per the policy below; widening is
  lossless, so `A -> B -> A` (B >= A) is the identity on all bit patterns.
- `ModeNumeric` — promises only the numeric value plus flags.

Explicit policies (both modes):

- **NaN payload**: widening places the payload (including the
  signaling/quiet bit) in the most significant mantissa bits; narrowing
  keeps the most significant payload bits and truncates the rest; if
  truncation would erase the whole payload, the lowest payload bit is set
  so a NaN never becomes a non-NaN.
- **Signaling bit**: always preserved; a signaling NaN is never quietly
  silenced. A signaling NaN operand raises `Invalid`.
- **Subnormals**: gradual underflow, never flush-to-zero. Exact subnormal
  results raise no flags; inexact ones raise `Underflow | Inexact`
  (tininess detected after rounding).

## Equality

`Equal` compares bit patterns: identical NaN patterns compare equal (host
`==` would say a NaN differs from itself) and `+0` differs from `-0`, so
the negative-zero sign stays observable.

## Verification

`go test ./...` runs:

- all **65536 float16 patterns**: encode/decode round trip in both byte
  orders, conversion to float32/float64 checked against an exact
  `math/big` integer reference, and lossless narrow-back identity;
- all **float32 midpoint boundaries** between adjacent float16 values
  (both signs): ties-to-even at the exact midpoint, plus/minus one float32
  ulp, cross-checked with the big.Int reference;
- randomized differential tests against the reference for every
  conversion pair;
- chunked-stream, endianness-switch, frame-validation, flag and
  NaN-payload policy tests.

Run the demo with `go run ./cmd/demo`.
