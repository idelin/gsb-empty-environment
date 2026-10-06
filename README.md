# eventbus

In-process Go event bus with replayable history and bounded
per-subscriber backlogs.

## Semantics

- `Publish(data)` appends to the bus history; sequence numbers start at
  1 and increase without gaps.
- `Subscribe(fromSeq)` first replays every event with `Seq > fromSeq`,
  then joins the live stream. The replay snapshot and the subscriber
  registration happen under the same lock, so events published during
  replay are neither lost nor duplicated at the boundary.
- Each subscription has a fixed backlog quota. A slow subscriber whose
  backlog would exceed the quota is explicitly disconnected: its channel
  is closed (buffered events remain readable), `Dropped()` reports true,
  and `LastDelivered()` gives the sequence number to resubscribe from
  for gap-free, duplicate-free recovery.
- `Cancel()` detaches the subscription and releases its backlog; it is
  idempotent and safe to call concurrently with `Publish`.

## Test

```sh
go test ./...
```

The tests verify the replay/live boundary, disconnect-reconnect
recovery, and backlog release against the full history slice
(`Bus.Snapshot`).
