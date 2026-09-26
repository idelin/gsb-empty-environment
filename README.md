# eliasfano

Elias-Fano encoding for non-decreasing `uint64` sequences in Go (standard
library only). Supports duplicates, O(1)-ish random access, exact-rank
`LowerBound`, bounded-memory streaming construction, and index-level merge
of two encoded sequences without full decoding.

## Encoding layout (real, verifiable)

For `n` values in `[0, U)`:

```
l          = max(0, floor(log2(U / n)))        low bits kept per value
low  bits  : n*l bits, packed into 64-bit words (little-endian)
high bits  : unary bucket gaps — the i-th value with high part h sets
             bit (h + i); n ones + numBuckets zeros
numBuckets = ceil(U / 2^l) <= 2n   (>= n when U > n)
```

The serialized form is self-describing and independently verifiable:

```
[8B]  magic "EFANO001"
uvarint n, universe, lowBits, numBuckets, lowWords, highWords
[8*lowWords]  low payload
[8*highWords] high payload
[8B]  CRC64-ISO over everything above
```

`Parse` re-derives every parameter from `(n, U)`, checks exact payload
length, high-bitvector popcount, zero tail bits, and the checksum — any
single-bit mutation is rejected, and forged headers cannot trigger
large allocations (all lengths are cross-checked before allocating).

## Space derivation

```
low   = n*l                     bits
high  = n ones + numBuckets zeros,  numBuckets <= 2n
      <= 3n                       bits
total <= n*(l + 3) + O(1)       bits
      ~= n*(log2(U/n) + 3)      bits per value
```

Measured occupancy (payload bits/value, see `go run ./cmd/efdemo`):

| n       | U    | l  | bits/value | bound l+3 |
|---------|------|----|-----------|-----------|
| 1000    | 2^10 | 0  | 2.05      | 3         |
| 1000    | 2^16 | 6  | 8.06      | 9         |
| 1000    | 2^24 | 14 | 16.06     | 17        |
| 10^5    | 2^32 | 15 | 17.31     | 18        |
| 10^6    | 2^40 | 20 | 22.05     | 23        |

`SizeBytes()` reports the exact serialized size; `Layout()` exposes the
structural parameters and byte accounting.

## Semantics

- `Access(i)` — random access by index.
- `LowerBound(v)` / `Rank(v)` — exact count of values `< v`; queries
  above the last value return `n` (past-the-end).
- `Count(v)` — exact multiplicity (duplicates supported).
- Empty sets encode to 22 bytes; a universe of 2^62 with 4 values takes
  70 bytes — memory depends on `n`, never on `U`.
- All-same-values and `U <= n` yield `l = 0` (empty low array, no
  division by zero anywhere).
- `Builder` streams values in non-decreasing order with memory bounded
  by the compressed output; `Merge` walks both high/low indexes
  directly (O(n1+n2) time, O(1) extra memory).

## Usage

```go
ef, _ := eliasfano.New([]uint64{1, 2, 2, 3, 5, 8, 13}, 256)
fmt.Println(ef.Access(3))        // 3
fmt.Println(ef.LowerBound(4))    // 4
fmt.Println(ef.Count(2))         // 2
blob := ef.Marshal()             // verifiable bytes
back, err := eliasfano.Parse(blob)
m := eliasfano.Merge(ef, other)  // index-level merge
```

## Test & demo

```
go test ./...     # reference-array, mutation, accounting, edge-case tests
go run ./cmd/efdemo
```
