# eventlog

Go state machine event log with verifiable compression.

## Design

- A log is a sequence of `Entry` values produced by `Execute`: each entry is
  an `Event` (`add`/`sub`/`mul`/`set`) plus, for public entries, an
  observation of the state right after the event.
- `Compress` keeps the initial state summary, the final state summary, and
  every public observation point, and attaches the full ordered list of
  original event summaries, run-length encoded as `Run`s. Events are merged
  only when consecutive and identical — never reordered or merged by type
  counts, because the operations do not commute.
- `CompressedLog.Verify` replays the attached event list from the initial
  summary and compares the replayed values against every recorded
  observation and the final summary. Dropped, reordered, or count-merged
  events make the replay diverge and are rejected.

## Tests

- `TestCompressVerifyRandomLogs`: random logs — compression verifies and
  replay is equivalent to direct execution.
- `TestNonCommutativeCountMergeRejected` / `TestCountMergeRejectedRandomLogs`:
  merging non-commuting ops by type counts is rejected.
- `TestCommutativeCountMergeAccepted`: boundary case — count merging is valid
  only when all ops commute and no intermediate observations exist.
- `TestBadCompressionsRejected`: tampered compressions are rejected.

Run: `go test ./...`
