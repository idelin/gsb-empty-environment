# tans — tabled Asymmetric Numeral Systems entropy coder (Go, stdlib only)

A finite-state entropy coding kernel that combines the compression ratio of
arithmetic coding with the throughput of Huffman coding. The entire
compression state is folded into a **single integer**; every streaming
push (`Encoder.Encode`) and pop (`Decoder.Decode`) is one lookup into a
precomputed state-transition table plus a few bit operations — near
constant time per symbol.

## Design

- **State table** (`Table`): symbol frequencies are normalized to a
  power-of-two size `M = 1<<tableLog` (default 4096 states), symbols are
  spread across the states with an odd-step cyclic walk, and the
  encode/decode transitions are derived from the resulting dyadic tiling of
  the state interval `[M, 2M)` (FSE-style construction).
- **Encoder** (`tans.go`): pushes symbols in reverse order; each push emits
  the low bits of the state and transitions via `encTable`. The final state
  is flushed as `tableLog` bits.
- **Decoder**: reads the bit stream backwards (LIFO), pops symbols in
  forward order; each pop reads `nbBits[state]` bits and transitions via
  `newState[state]`.
- **Container format** (`Compress`/`Decompress`): 5-byte magic, tableLog,
  uvarint original length, sparse normalized-count table, payload, 1 pad
  byte.
- **Baseline** (`arith_ref.go`): a textbook bit-oriented arithmetic coder
  (Witten-Neal-Cleary) used only for ratio/throughput comparison.

## Files

- `tans.go` — table construction, `Encoder`/`Decoder`, `Compress`/`Decompress`
- `bitstream.go` — LIFO bit writer/reader
- `arith_ref.go` — reference arithmetic-coding baseline
- `tans_test.go` — round-trip, ratio, corruption tests + benchmarks
- `cmd/tans-demo/main.go` — runnable demo

## Run

```sh
go test ./...                 # unit tests
go test -run xxx -bench . .   # benchmarks
go run ./cmd/tans-demo        # demo on a 4 MiB highly-skewed sample
```

## Test results (Apple M1 Pro, go1.24.2, darwin/arm64)

```
=== RUN   TestCompressionRatioNearEntropy
    entropy bound: 943290 bits, tANS: 944152 bits (0.09% overhead, header included)
    arithmetic coding payload: 117915 bytes, tANS total: 118019 bytes
--- PASS: TestCompressionRatioNearEntropy
ok  	tans	0.427s   (all tests)
```

Benchmarks on a 1 MiB highly-skewed sample (`p(i) ∝ 0.8^i`, 32 symbols):

```
BenchmarkTANSEncode-10    135.78 MB/s
BenchmarkTANSDecode-10    110.68 MB/s
BenchmarkArithEncode-10    27.06 MB/s
BenchmarkArithDecode-10    22.49 MB/s
```

Demo output (`go run ./cmd/tans-demo`, 4 MiB skewed sample):

```
input: 4194304 bytes, order-0 entropy bound: 1887263 bytes (3.600 bits/symbol)

codec              size (bytes)  vs entropy   encode      decode
tANS (this)            1887806    +0.03%     116.4 MB/s    100.0 MB/s
arithmetic (ref)       1888002    +0.04%      26.6 MB/s     22.3 MB/s

speedup tANS vs arithmetic: encode 4.4x, decode 4.5x
```

tANS matches the theoretical entropy bound within ~0.1% (on par with the
arithmetic coder) while running ~4-5x faster in both directions.
