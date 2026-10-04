# wsample

Weighted random sampling **without replacement** over a mutable set, in Go.

- **Non-negative integer weights, arbitrary precision.** Weights and weight
  sums are `math/big.Int` — no overflow, no floating-point rounding in the
  distribution. Zero-weight items are never selected.
- **Caller-supplied randomness.** You provide the random byte stream
  (`Source`); the sampler is fully deterministic given the same bytes, so
  fixed streams replay exactly.
- **No modulo bias.** Uniform values in `[0, total)` come from rejection
  sampling over whole bytes (spare high bits masked off), never from `x % n`.
- **Transactional draws.** `Draw(k)` either completes or leaves everything
  untouched: the item set is never modified by a draw, and random bytes
  consumed by a cancelled draw are buffered and replayed on the next call,
  so the random stream resumes at exactly the position where the cancelled
  draw started. No half-drawn, half-rolled-back states.

## Usage

```go
s := wsample.New(wsample.ReaderSource{R: rand.Reader}) // any wsample.Source
s.Add(big.NewInt(10)) // -> index 0
s.Add(big.NewInt(0))  // -> index 1, zero weight: never selected
s.Add(big.NewInt(30)) // -> index 2
s.SetWeight(0, big.NewInt(60)) // weights are mutable

picks, err := s.Draw(2) // 2 distinct indices, P(item) ∝ weight
```

Cancellation: have your `Source.Fill` return an error (e.g. on
`context.Canceled`). `Draw` returns that error with no result and no state
change; call `Draw` again later to resume with the same logical random
stream. `Source.Fill` must fill the buffer completely or report the bytes as
unconsumed on error.

## Correctness

The test suite verifies the implementation three ways:

- **Exact probabilities by enumeration.** For small weight sets (including
  zero weights) the whole random domain is enumerated and the empirical
  outcome distribution must equal the theoretical
  `P(i then j) = w_i/T · w_j/(T−w_i)` exactly (compared as `big.Rat`).
- **Deterministic replay.** Fixed byte streams reproduce identical draw
  sequences and identical stream consumption, across weight mutations and
  across draws with weights beyond 64 bits.
- **Cancel/resume.** A draw cancelled mid-stream (repeatedly) leaves the set
  state byte-for-byte intact and, once resumed, produces exactly the result
  and stream position of an uninterrupted run.

```sh
go test ./...
```
