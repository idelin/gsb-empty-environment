# adaptivehuffman

Byte-oriented adaptive prefix coding (FGK-style adaptive Huffman) for
streaming data, Go standard library only. The input is encoded one byte at a
time with no preliminary pass; encoder and decoder maintain identical
adaptive code trees from the same initial state, so no dictionary is ever
transmitted.

* First occurrence of a byte: escape (NYT) code + raw 8 bits.
* Deterministic updates: stable node ids, highest-rank equal-weight swap
  rule, weight rescaling on overflow — specified in [FORMAT.md](FORMAT.md).
* Self-contained frames: header carries the original length and a
  format/state version; fixed zero-padding at the end; precise byte/bit
  error offsets for invalid escapes, truncated codewords, non-padding bits
  and trailing data.
* Full state export/restore: resuming at any byte boundary is bit-identical
  to an uninterrupted run.

## Usage

```go
// Frame API (self-contained, default parameters):
var buf bytes.Buffer
err := adaptivehuffman.EncodeFrame(&buf, data)
back, err := adaptivehuffman.DecodeFrame(&buf)

// Streaming API:
enc := adaptivehuffman.NewEncoder()
bw := adaptivehuffman.NewBitWriter(w)
for _, b := range data {
    err := enc.EncodeSymbol(bw, b)
}
err = bw.Flush() // zero-pads the final byte

dec := adaptivehuffman.NewDecoder()
br := adaptivehuffman.NewBitReader(r)
sym, err := dec.DecodeSymbol(br)

// State export / restore:
state := enc.ExportState()
enc2, err := adaptivehuffman.RestoreEncoder(state) // continues bit-identically
```

## Tests

```sh
go test ./...
```

Tests include exhaustive round-trips of all short strings over small
alphabets with per-symbol structural invariant checks (prefix-freeness,
weights, numbering) and encoder/decoder state comparison, all 256 byte
values, long single-byte streams, chunked bit-stream input, forced rescale
runs, error-offset checks, and state export/restore at every symbol
boundary.
