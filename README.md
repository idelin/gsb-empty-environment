# resample

Streaming sample-rate conversion in Go: rational rate ratios, linear
interpolation, exact rational phase on an absolute time axis.

## Pieces

- **`Resampler`** — single-rate streaming converter. `New(num, den)` sets
  the ratio output-rate/input-rate. `Write` accepts arbitrarily sized
  chunks; phase is never reset, so any chunking yields identical output.
  `Flush` ends the stream: outputs whose interpolation window runs past the
  last input sample are completed by repeating that sample. Empty input
  produces no output. The internal buffer holds at most a couple of samples
  regardless of stream length.
- **`Sequencer`** — reassembles the contiguous confirmed prefix from
  fragments that carry original sample indices and may arrive out of order,
  duplicated, or overlapping. Gaps are held back, never zero-filled. Memory
  is bounded by the reordering window, not the stream length.
- **`Switcher`** — multi-rate converter. The rate ratio changes at specified
  input-sample indices; the output schedule lives on an absolute rational
  time axis (positions in input-sample units), so phase carries across
  switches without reset. `Snapshot`/`Restore` provide checkpoints that
  replay bit-identically.

## Semantics

Output positions (input-sample units) are `pos_0 = 0`,
`pos_{k+1} = pos_k + step(pos_k)` with `step = den/num` of the active
segment. A stream of `N` samples yields every output with `pos_k < N`,
i.e. `ceil(N*num/den)` outputs for a single rate.

## Tests

`go test ./...` checks streamed output against a one-shot exact rational
reference model (`math/big`) across arbitrary chunkings, out-of-order and
duplicated fragments, rate switches, and checkpoint restore, and asserts
buffers stay constant while streaming.
