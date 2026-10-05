# log-slicer

Backward slicer for event logs. Starting from a final failing assertion, it
traces data and control dependencies backwards through the log, keeps the
causal provenance of every included event, reports missing producer events as
explicit gaps, and re-executes the slice to verify that the assertion value
reproduces.

## Log format

A log is a JSON object with an `events` array. Each event:

| field     | meaning                                                        |
|-----------|----------------------------------------------------------------|
| `id`      | unique event id, in log order                                  |
| `kind`    | `const` \| `op` \| `branch` \| `assert`                        |
| `writes`  | `{name, version}` written by this event, or `null`             |
| `reads`   | `[{name, version}]` data dependencies (versioned, SSA-style)   |
| `control` | `[eventId]` control dependencies (e.g. guarding branches)      |
| `op`      | operator name used to recompute the value during replay        |
| `value`   | recorded value (written value / branch outcome / assert result)|

## Why versions, not names

Dependencies are keyed by `(name, version)` pairs, never by variable name
alone. If `x@0 = 40` is later shadowed by `x@1 = 7`, an assertion that
causally depends on `x@0` slices to the producer of `x@0` only. A slicer that
merged by name would pull in `x@1` and replay to a different assertion value.
See `examples/failing.json`: event 5 (`x@1`) is correctly excluded, and the
provenance edges show event 7 depends on event 2 via `x@0`.

## Slicing

`sliceLog(log, assertionId)` walks a worklist backwards from the assertion:

- every read `(name, version)` resolves to the unique event that writes that
  exact version; if no event writes it, a `missing-producer` **gap** is
  recorded (with the needing event id and the missing variable);
- every control dependency resolves to its event; if absent from the log, a
  `missing-control` **gap** is recorded;
- both dependency kinds are followed transitively.

The result contains the sliced events (in log order), the provenance edges
(`from --data x@0--> to` / `from --control--> to`), and the gap list.

## Replay verification

`replaySlice(slice)` re-executes the sliced events in log order using the
operator table in `src/ops.ts` (`const` events anchor on their recorded
value). Values that depend on a gap become *unknown* and propagate. The
recomputed assertion value is compared against the recorded one:

- `match` — replay reproduces the recorded assertion value;
- `mismatch` — replay computes a different value (log inconsistency);
- `indeterminate` — gaps made the assertion value uncomputable.

## Usage

```sh
npm install
npm test                                   # build + run test suite
node dist/src/cli.js examples/failing.json # slice + replay, exit 0 on match
node dist/src/cli.js examples/gapped.json  # reports gaps, exit 3 (indeterminate)
```

CLI exit codes: `0` replay match, `2` mismatch, `3` indeterminate, `1` usage
error. Without `--assert <id>` the criterion defaults to the last failed
assertion in the log.

## Layout

- `src/types.ts` — log, slice, gap, and replay-report types
- `src/ops.ts` — replayable operator table
- `src/slicer.ts` — version-aware backward slicer with gap detection
- `src/replay.ts` — slice re-execution and assertion verification
- `src/cli.ts` — command-line entry point
- `test/run-tests.ts` — test suite (plain runner, no framework)
- `examples/` — a complete failing log and a truncated (gapped) log
