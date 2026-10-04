# imgcodec

Instruction-based image codec in Go. An image (row-major bytes) is encoded
as a sequence of three instruction types, each with a fixed header cost:

| Instruction | Effect | Cost |
|---|---|---|
| `Literal` | emit `Len` raw pixels | `LitHeader + Len` |
| `Repeat`  | emit `Len` copies of one pixel | `RepHeader + 1` |
| `Ref`     | copy `Len` pixels from the previous row, same columns | `RefHeader` |

References are bounded (`Len <= MaxRefLen`) and never cross a row boundary;
row 0 cannot be referenced. Literals and repeats may span rows.

## Optimal encoding

`Encode(pix, p)` returns a minimum-cost instruction sequence for the whole
image via a shortest-path DP over pixel positions. Two observations keep it
exact while running in O(N) time:

- `dp[j]` (cheapest cost of the suffix starting at pixel `j`) is
  non-increasing in `j`, and repeats/references have length-independent
  cost, so both are only ever taken at their maximal feasible length.
- Adjacent literals always merge cheaper, so a literal ends only at the
  image end, the wire-length cap (65535), or a position where a run of
  `>= 2` equal pixels or a reference match begins. Since a literal ending
  at `j` costs `LitHeader - i + (j + dp[j])`, the best end inside the
  sliding candidate window is tracked with a monotone queue.

`brute_test.go` cross-checks the pruned DP against an exhaustive search
that tries every legal instruction length at every position (thousands of
random images and cost models, including zero headers).

## Wire format

All integers little-endian; `len` is `u16`, always `>= 1`.

```
Literal: [0x00][len][payload len bytes]
Repeat:  [0x01][len][value]
Ref:     [0x02][len]
```

`DefaultParams` (headers 3/3/3) matches the wire overhead, so
`Params.Cost(instrs) == len(Marshal(instrs))`.

## Strict decoding

`Decode(data, p)` validates the stream instruction by instruction and
returns exactly `Width*Height` pixels. Any violation — truncated header or
payload, zero length, unknown opcode, reference in the first row,
reference longer than `MaxRefLen`, reference crossing a row, pixel-count
over- or underflow — is reported as a `*DecodeError` carrying the
offending instruction's index and byte offset. Corrupt input never
produces a partial pixel buffer.

```go
type DecodeError struct {
    Instr  int // index of the offending instruction
    Offset int // byte offset where it starts
    Reason string
}
```

## Usage

```go
p := imgcodec.DefaultParams(width, height)
data, err := imgcodec.EncodeImage(pix, p) // optimal encode + marshal
pix2, err := imgcodec.Decode(data, p)     // strict decode
```

## Tests

```
go test ./...
```

Covers hand-computed optima, exhaustive-vs-DP cost equality, encode/decode
round trips, instruction-legality invariants, wire-length/cost equality,
a table of malformed streams checked for exact instruction location, and
random-input decode robustness.
