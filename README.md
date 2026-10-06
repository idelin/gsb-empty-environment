# resample

Streaming resampling in Go with exact rational phase, linear interpolation,
fragment reassembly, and sample-rate switching on an absolute rational
timeline.

## Semantics

### Fixed ratio (`Resampler`)

`New(stepNum, stepDen)` sets the exact rational ratio of `stepNum/stepDen`
input samples per output sample. Output `n` sits at input time
`t_n = n*stepNum/stepDen` and is linearly interpolated.

- Outputs exist while `t_n < N` (`N` = total input samples), so the output
  count is exactly `ceil(N*stepDen/stepNum)`.
- Tail outputs whose right interpolation point lies past the end of the
  stream repeat the last sample.
- Empty input produces no output.
- Phase is exact integer arithmetic carried across `Write` calls: any
  chunking of the same stream yields bit-identical output. Chunked input
  never resets state.
- Internal buffering is O(1) — at most two input samples are retained,
  independent of stream length (`Buffered()` exposes this for tests).

### Fragments (`Sequencer`)

Input fragments carry the absolute index of their first sample and may
arrive out of order, overlapping, or duplicated. The sequencer releases
only the contiguous confirmed prefix; samples behind a gap are held, and
gaps are never zero-filled. Pending memory is bounded by the reordering
window, not the confirmed stream length.

### Rate switching (`Timeline`)

`NewTimeline(outRate, segs...)` resamples on an absolute rational time
axis. `InputSegment{FromIndex, Rate}` switches the input sample rate at an
absolute sample index; sample times are continuous across the switch and
the output phase is a single exact rational carried through it, so phase
passes continuously in absolute time. The tail rule (repeat last sample),
empty-input rule, and O(1) buffering match the fixed-ratio resampler.

### Checkpoints

`Timeline.Checkpoint` / `RestoreTimeline` and `Sequencer.Checkpoint` /
`RestoreSequencer` snapshot the full state (deep-copied). Restoring into
fresh instances and continuing with the remaining input yields output
identical to the uninterrupted run.

## One-shot reference models

`ResampleAll` and `ResampleTimeline` compute the same results by reading
the whole input at once with exact rational arithmetic. The tests verify
the streaming implementations against them bit-for-bit under arbitrary
chunkings, out-of-order/duplicated fragment delivery, rate switches, and
checkpoint/restore cycles.

## Example

```go
r := resample.New(160, 147) // 44.1k -> 48k
var out []float64
for _, chunk := range chunks {
    out = append(out, r.Write(chunk)...)
}
out = append(out, r.Flush()...) // tail repeats the last sample
```

```go
seq := resample.NewSequencer()
tl := resample.NewTimeline(
    resample.Rate{Num: 48000, Den: 1},
    resample.InputSegment{FromIndex: 0, Rate: resample.Rate{Num: 44100, Den: 1}},
    resample.InputSegment{FromIndex: 1 << 20, Rate: resample.Rate{Num: 32000, Den: 1}},
)
for _, frag := range fragments { // any order, duplicates ok
    out = append(out, tl.Write(seq.Add(frag))...)
}
out = append(out, tl.Flush()...)
```

## Tests

```sh
go test ./...
```
