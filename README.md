# streamagg

Per-partition watermark aggregator for Go streams.

## Semantics

- The **global watermark** is the minimum watermark across **active**
  partitions. It is monotonic and never moves backwards.
- A partition is excluded from the minimum **only** when explicitly marked
  idle via `MarkIdle`. A stalled but active partition holds the global
  watermark back; an active partition that has not reported any watermark
  blocks advancement entirely.
- When **all** partitions are idle, the global watermark keeps its previous
  value.
- A resumed partition (`MarkActive`) retains its last watermark. Events
  arriving below the current global watermark are routed to the **late
  channel** (`Late()`) and never regress the global watermark.
- **Checkpoints** (`Snapshot`/`SaveCheckpoint`/`LoadCheckpoint`) persist the
  global watermark plus every partition's watermark and idle flag, so event
  replay across a restore reproduces the exact watermark sequence.

## Example

```go
a := streamagg.New(64)
a.AddPartition("p1")
a.AddPartition("p2")

a.OnEvent(streamagg.Event{Partition: "p1", Timestamp: 10})
a.OnEvent(streamagg.Event{Partition: "p2", Timestamp: 20})
// global = 10

a.MarkIdle("p1")
// global = 20

a.MarkActive("p1")
a.OnEvent(streamagg.Event{Partition: "p1", Timestamp: 15})
// 15 < 20: routed to a.Late(), global stays 20
```

## Test

```sh
go test ./...
```
