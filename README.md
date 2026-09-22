# fpconv — configurable binary floating-point conversion kernel

A Go implementation of conversion between user-defined binary floating-point
formats. The kernel never routes values through a host floating-point type
(no `float32`/`float64` arithmetic, no `strconv.ParseFloat` on the production
path): every rounding decision is made on bit strings and exact integers.

## Formats

A format is described by four parameters (`fp.NewFormat`):

- `ExpBits` — exponent field width
- `MantBits` — trailing-significand field width (precision `p = MantBits+1`)
- `Bias` — exponent bias
- fixed special-value rules: biased exponent 0 is zero/subnormal, biased
  exponent `2^ExpBits-1` is Infinity (mantissa 0) or NaN (mantissa ≠ 0), and
  the most significant mantissa bit is the NaN quiet bit.

Built-ins: `Binary16` (5/10/15), `BFloat16` (8/7/127), `Binary32`
(8/23/127); `Binary64` is also provided for cross-checks. Total width must be
a multiple of 8 and at most 64 bits.

## Rounding and flags

- Four IEEE 754 directions: ties-to-even, toward zero, toward +Inf,
  toward −Inf.
- Per-element flags: `invalid`, `overflow`, `underflow`, `inexact`.
- **Underflow** uses the "rounded result is tiny and inexact" definition: the
  rounded magnitude is below `2^emin` (the smallest normal) **and** it differs
  from the exact value. An exact subnormal never flags underflow; a value that
  rounds up to the smallest normal is not tiny and does not flag underflow.
- **Overflow** is raised when the rounded magnitude exceeds the largest finite
  value; ties-to-even at `2^(emax+1)` goes to Infinity, toward-zero returns the
  largest finite value, and the directed modes return ±max-finite or ±Inf per
  sign.

## Zeros and NaNs

- Positive and negative zero are preserved through every conversion and mode.
- qNaN passes through; sNaN is converted to qNaN and raises `invalid`.
- The sign bit of a NaN is preserved.
- Payloads occupy the low `MantBits-1` bits. Widening zero-extends; narrowing
  truncates the high payload bits. The quiet bit is always set on output.
- Decimal `nan[(payload)]` is always parsed quiet; a signaling NaN is never
  synthesized from text.

## Direct decimal conversion

`ConvertString` accumulates decimal digits into `math/big.Int` powers of ten
and rounds the resulting rational directly into the destination. There is no
binary64 intermediate, so values cannot be double-rounded.

### Double-rounding counterexample

```
1.00048828125000000000008272
```

The binary16 numbers around 1.0 are `1` (`0x3c00`) and `1+2^-10` (`0x3c01`),
whose midpoint is `m = 1+2^-11 = 2049/2048 = 1.00048828125`.

The literal is `m + δ` with `0 < δ < 2^-64` (verified exactly in
`TestDoubleRoundingCounterexample`):

- **Direct to binary16:** the value is strictly above `m`, so it rounds up to
  `0x3c01`.
- **Via binary64:** the binary64 spacing at `m` is `2^-63`; half of that is
  `2^-64`, and `δ < 2^-64`, so binary64 rounds the literal *down* to the exact
  midpoint `m` (`0x3fe0000000000000`). Narrowing `m` to binary16 is an exact
  tie; the two unit significands are 2048 (even) and 2049 (odd), so
  ties-to-even picks `0x3c00`.

Direct conversion yields `0x3c01`; binary64-then-binary16 yields `0x3c00`.

## Kernel organization

- `format.go` — format definition, packing/unpacking, built-in formats.
- `value.go` — exact rational value type and the direct decimal parser.
- `round.go` — arbitrary-precision production kernel (`math/big.Int` division
  with explicit remainder/half/above classification), used for decimals.
- `roundbits.go` — allocation-free native integer path for format-to-format
  conversion (`n·2^k` inputs), validated exhaustively against `round.go` and
  the reference.
- `reference.go` — independent `math/big.Rat` reference: for narrow formats it
  enumerates the entire destination grid and compares exact midpoint
  rationals; for wider formats it locates neighbors with rational floor
  division. It shares no rounding code with the production kernel.
- `batch.go` — packed-buffer batch conversion with overlap-safe semantics.

## Batch conversion

`Batch(dstFmt, srcFmt, dst, src, count, mode)` converts little-endian packed
buffers. Source and destination may partially overlap: when the byte ranges
share memory, the source is snapshotted first, so the result is exactly what
you would get by reading the complete source sequence before writing.

Structural errors (nil/short buffers, negative count) are detected before any
write; on error the destination is left untouched.

## Verification

- Exhaustive all 65536 binary16 bit patterns: finite values survive
  widen-to-binary32/binary64-then-narrow with bit-identical results; every
  NaN is checked for payload preservation/truncation, quieting, sign, and the
  `invalid` flag.
- Exhaustive bfloat16 widen/narrow identity.
- For binary16 and bfloat16, every pair of adjacent grid values is probed with
  the exact midpoint, midpoint + gap/16, midpoint − gap/16, and the endpoints,
  in both signs and all four rounding modes — covering every within-binade
  pair, every binade boundary, the subnormal region, and the overflow edge.
- Representative f32 and f64 midpoint/sampling cases (full enumeration is
  infeasible there), plus strided cross-format checks, all compared against
  the independent `big.Rat` reference.

Run:

```
go test -count=1 ./...
```

## Reproducible throughput experiment

```
go test -run XXX -bench 'BenchmarkBatch' -benchmem -count=3 ./fp
```

Reference result, Apple M1 Pro (darwin/arm64), Go 1.24:

```
BenchmarkBatchF32ToF16-10   ~210 MB/s   65536 B/op   1 alloc/op
BenchmarkBatchF64ToF32-10   ~440 MB/s   65536 B/op   1 alloc/op
```

The single allocation is the returned per-element `[]Flags`; disjoint source
and destination buffers otherwise do not allocate. Overlapping buffers add one
source snapshot. `bench.sh` reruns the suite and the benchmark with a local
build cache.
