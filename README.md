# telemetry

Fixed-bucket telemetry aggregation in Go (standard library only).

- Per-bucket `min/max/sum/count` plus first/last, with event-level evidence
  for every extreme.
- Bucket boundaries use floor division; an event exactly on a boundary
  opens the new bucket. Empty buckets are gaps (`Present == false`),
  distinct from buckets that hold real zero values.
- Ingest dedups by event ID, so overlapping batch redelivery is safe.
- Versioned interval corrections (`Correct`) and deletions (`Delete`):
  affected bucket summaries are rebuilt from surviving details, so a
  retracted peak is replaced by the next-largest value. Old snapshots stay
  queryable via `QueryAsOf`.
- Queries serve internal buckets from maintained summaries and edge
  buckets from raw details — no double counting. First/last order by
  event time with ID tie-break. Mean is always `sum/count`.
- Every mutation appends an audit entry: buckets recomputed and the
  detail range scanned.
- Fixtures synthesize events locally (`Generate`, `SampleScript`) and are
  replayable with `Replay`. No monitoring platform, no dashboard.

## Run

```sh
go test ./...
go run ./cmd/demo
```
