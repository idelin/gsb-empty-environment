# lpc — length-limited prefix codes (Go, standard library only)

Build an optimal prefix code when **every code length must be ≤ L**, reject
the alphabet when it physically cannot fit, and report the exact Kraft sum
and weighted code-length cost. No compression library is used: the
lengths are computed from scratch with the **package-merge** algorithm (an
exact length-constrained optimum — *not* a truncated ordinary Huffman
tree), and code words are assigned canonically from the lengths.

## Guarantees

- **Feasibility check.** `n` symbols need `n ≤ 2^L` leaf slots. When
  `2^L < n`, `Build` returns `ErrInfeasible` and no code is produced.
- **All lengths ≤ L.** Verified after construction and again when parsing
  a header.
- **No truncation collisions.** A plain Huffman tree cut off at depth `L`
  can reuse an already-assigned short word (e.g. weights
  `{100,1,1,1}`, Huffman lengths `{1,3,3,2}`; naïvely forcing depth 2
  gives `{1,2,2,2}`, Kraft = 5/4 and the depth-2 word `11` collides).
  Package-merge returns the true optimum `{2,2,2,2}`. The header parser
  independently rejects tables that violate Kraft equality.
- **Kraft check.** `Σ 2^-len(symbol)` is computed exactly with
  `math/big`; every emitted/accepted code equals **1** (complete prefix
  code).
- **Weighted length cost.** exact `Σ w·l / Σ w` returned as integer
  numerator/denominator plus a 256-bit `big.Float`.
- **Explicit format.** magic `LCPC`, format version `1`, maximum length,
  symbol count, per-symbol lengths, CRC-32/IEEE.
- **Bounded decode.** `Decode(data, maxOut)` refuses a frame whose symbol
  count exceeds the caller's quota **before allocating output**
  (`ErrOutputLimit`); corrupt frames fail CRC/padding/bitstream checks.
- Standard library only; tests run under `go test`.

## Layout

| file | contents |
| --- | --- |
| `lpc.go` | package-merge `Build`, canonical word assignment, Kraft/cost |
| `format.go` | versioned, CRC-protected code header (marshal/unmarshal) |
| `codec.go` | MSB-first bit packer, self-describing frame, bounded decoder |
| `cmd/demo` | runnable end-to-end examples |
| `*_test.go` | feasibility, optimality vs exhaustive DP, Huffman agreement, format/CRC, bounded decode |

## Header format (v1, big-endian)

```
0  4  magic "LCPC"
4  1  format version = 1
5  1  maximum code length L
6  1  reserved (must be 0)
7  4  symbol count N
11 N  code length per symbol (symbol order)
11+N 4  CRC-32/IEEE over bytes [0, 11+N)
```

A message is a header followed by one frame:

```
4 bytes  symbol count M (big-endian)
k bytes  MSB-first bit-packed canonical words, byte aligned (zero pad)
4 bytes  CRC-32/IEEE over the count bytes and the packed payload
```

Code words are deliberately not stored: they are canonical, derived from
lengths only. A one-symbol alphabet uses the empty word (length 0) and an
empty payload.

## Run

```
go run ./cmd/demo
go test ./... -v
```

## Sample output

```
== case 1: build a code under L = 4 ==
symbols=5  L=4
lengths : 4 4 3 2 1
words   : 1110 1111 110 10 0
kraft   : 1 (must be exactly 1)
weighted length cost: 56/31 = 1.806452 bits/symbol
header  : 4c435043010400000000050404030201df093a1f  (20 bytes, magic="LCPC" version=1, CRC-32 last 4 bytes)

== case 2: reject when 2^L < number of symbols ==
Build(5 symbols, L=2) -> lpc: infeasible: 2^L < number of symbols

== case 3: length limit binds; truncated Huffman would collide ==
Huffman, L=3: lengths [1 3 3 2], cost 1.0485 bits/symbol
  symbol 0 -> 0 (len 1)
  symbol 1 -> 110 (len 3)
  symbol 2 -> 111 (len 3)
  symbol 3 -> 10 (len 2)
  naive truncation to 2 bits reuses word 11 for two symbols: collision
package-merge, L=2: lengths [2 2 2 2], kraft 1, cost 2.0000 bits/symbol
  symbol 0 -> 00 (len 2)
  symbol 1 -> 01 (len 2)
  symbol 2 -> 10 (len 2)
  symbol 3 -> 11 (len 2)
truncated table {1,2,2,2} rejected by UnmarshalCode: lpc: bad code header

== case 4: bounded decode quota ==
encoded 6 symbols into 29 bytes (self-describing)
Decode(data, quota=8) -> [0 1 2 3 0 1]
Decode(data, quota=5) -> lpc: decoded output quota exceeded
Decode(tampered)      -> lpc: checksum mismatch
```

## Test results

`go test ./... -v` — all tests PASS:

- `TestClassicFiveSymbols` — lengths/Kraft/exact 56/31 cost
- `TestInfeasible` — `(n,L)` = (5,2), (9,3), (2,0), (63,5) rejected
- `TestLengthBoundBinds` — L=2 forces `{2,2,2,2}`; cost exact
- `TestMatchesPlainHuffmanWhenLimitLoose` — package-merge = Huffman at L=62
- `TestOptimalByExhaustiveDP` — matches brute-force minimum over every
  feasible length vector (small alphabets), including an infeasible case
- `TestZeroWeightSymbols`, `TestBadArguments`, `TestSingleSymbol`
- `TestEncodeDecodeRoundTrip`, `TestDecodeQuotaBounded`,
  `TestSingleSymbolCodec` — codec and output quota
- `TestMarshalRoundTripAndFields`, `TestCorruptionDetected`,
  `TestInvalidLengthTableRejected`, `TestFrameCorruptionAndPadding` —
  magic/version/reserved/CRC/Kraft and padding validation
- `TestLargeAlphabet` — n=4096, L=15: Kraft-complete, bound respected,
  header round-trips
