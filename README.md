# prefixcode

Deterministic canonical prefix codes in Go (standard library only, no
compression libraries).

## Features

- **Optimal code lengths**: `BuildLengths` uses the exact package-merge
  algorithm, minimizing `sum(freq*len)` subject to a maximum code length.
  Deterministic; equal frequencies are tie-broken by byte order (smaller
  byte gets the shorter-or-equal code).
- **Edge cases**: empty input yields an empty table; a single symbol gets
  a real 1-bit code (never an undecodable empty code).
- **Validation**: `Validate` detects oversubscribed (Kraft > 1) and
  incomplete (Kraft < 1) tables. `Kraft` returns the exact sum as a
  `big.Rat`; `Cost` and `Entropy` give the optimal-cost and
  information-theoretic references.
- **Canonical codes**: `CanonicalCodes` assigns codes by (length, symbol)
  order, so identical lengths always give identical codes.
- **Streaming decoder**: layered 8-bit lookup tables (one level per 8
  code bits, so memory scales with the alphabet, not `2^maxLen`). State
  persists across calls; input may be chunked at arbitrary byte
  boundaries. At `finish`, an incomplete trailing codeword reports
  `ErrTruncated` while a bit pattern matching no codeword reports
  `ErrInvalidCode` — a truncated tail is never accepted as success.
- **Bounded output**: the decoder quota is shared across calls
  (`WithQuota`/`AddQuota`); `ErrShortBuffer` and `ErrQuota` are resumable.
- **Wire format**: magic, version byte, maxLen, 256 code lengths, symbol
  count, payload length, and a CRC-32 covering header *and* payload, so
  tampered tables, lengths, or payloads are rejected deterministically.

## Usage

```go
data, err := prefixcode.Compress(message, 12)   // build + encode
out, err := prefixcode.Decompress(reader, 1<<20) // decode with quota
```

Low-level streaming:

```go
tbl, _ := prefixcode.NewTable(lengths)
dec := prefixcode.NewDecoder(tbl,
    prefixcode.WithSymbolCount(int64(n)),
    prefixcode.WithQuota(int64(n)))
consumed, produced, err := dec.Decode(chunk, dst, finish)
```

## Demo and tests

```
go run ./cmd/pcdemo   # sample run: table, Kraft sum, cost vs entropy
go test ./...         # includes brute-force optimality verification
```
