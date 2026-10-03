# adaptivehuffman

FGK (Faller–Gallager–Knuth) adaptive Huffman codec in pure Go (standard
library only).

## Properties

- **Single streaming pass** — no preliminary scan of the input; the tree
  is built and rebalanced on the fly, one byte at a time.
- **Sibling property** — after every symbol the tree is updated so that
  nodes enumerated by decreasing order number have non-increasing
  weights and siblings occupy adjacent numbers (verified in tests).
- **NYT marker** — previously unseen bytes are introduced through a
  Not-Yet-Transmitted node: the encoder emits the NYT code followed by
  the raw 8-bit value, then splits the NYT node.
- **Synchronized decoder** — the decoder performs the identical updates
  while reading the bit stream, so both trees always match and no
  dictionary or frequency table is ever transmitted.
- **Constant memory** — the alphabet is 256 byte values, so the tree
  never exceeds 2·256+1 = 513 nodes regardless of input length.

## Usage

```go
var buf bytes.Buffer
enc := adaptivehuffman.NewEncoder(&buf)
enc.Write(data)   // any number of times, any chunk sizes
enc.Close()       // flush the final partial byte

dec := adaptivehuffman.NewDecoder(&buf)
plain, err := io.ReadAll(dec) // byte-identical to data
```

`Encoder` implements `io.Writer`, `Decoder` implements `io.Reader`.

## Demo

```
$ go run ./cmd/ahuffdemo
input:            4194304 bytes (single-pass stream, skewed distribution)
compressed:       1926244 bytes (ratio 0.459, 3.7 bits/byte)
tree nodes:       115 (bounded by 2*256+1 = 513 => O(1) memory)
decoded:          4194304 bytes, decoder tree nodes: 115
verification:     OK - decoded output is byte-identical to the input
```

## Tests

```
go test -v ./...
```

Covers: round-trip of a 1 MiB skewed stream, chunked streaming I/O,
sibling-property invariant after every byte, encoder/decoder tree
synchronization, constant memory bound, empty input, single repeated
symbol, all 256 byte values, and adaptive codeword shortening.
