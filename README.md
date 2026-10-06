# eventlog

A Go library for compressing deterministic state-machine event logs into a
verifiable form, and a validator that replays the compressed log to check
faithfulness.

## Model

Events drive a small state machine (`machine.go`):

- `OpAdd` / `OpMul` — commutative, associative arithmetic on the state value.
- `OpSet` — overwrite; commutes with nothing.
- `OpObserve` — a public observation point recording (seq, tag, value).

## Compression (`compress.go`)

`Compress(initial, events)` produces a `CompressedLog` keeping:

- the **initial summary** and **final summary** (value, applied-event count,
  observation count, FNV-1a hash);
- **every public observation point** (sequence index, tag, observed value);
- per-segment **merged events** plus the **replayable summaries of the
  original events** (`Segment.Originals`), observes included as the closing
  entry of each segment.

Only maximal runs of the *same* commutative operation merge (adds sum,
muls multiply). Any kind change — and any `OpSet` — closes the run:
non-commutative operations are never merged by type counts alone, because
their relative order determines the result.

## Validation (`validate.go`)

`Validate` replays **both** the original event summaries and the merged
events from the initial summary, and rejects the log unless:

- both replays agree segment by segment (value and covered event count);
- every recorded observation is reproduced exactly (position, tag, value);
- the final summary (hash included) matches the replay.

## Tests (`compress_test.go`)

- Randomized losslessness: 300 random logs compress, validate, and reproduce
  every observation and the final summary, never growing the log.
- Bad-compression rejection: a faulty compressor that merges by per-type
  counts (ignoring non-commutative ordering) is run over 500 random logs;
  the validator rejects exactly the outputs whose merged stream diverges
  (424/500) and accepts the harmlessly-reordered rest — no false positives.
- Tampering table: corrupted summaries, observations, originals, merged
  events and segment structure are all rejected.

## Run

```sh
go test ./...
```

(On Go 1.11 toolchains: `GO111MODULE=on GOCACHE=/tmp/gocache go test ./...`)
