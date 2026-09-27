# reordersim

Discrete-event simulation of a reorder buffer in Go (standard library only).
All packets, queues and links run inside a discrete-event model over integer
ticks; no real network traffic is generated.

## Semantics

- **Release order**: packets are released by `(EventTime, Seq)` — event time
  first, sequence number breaks ties at the same event time.
- **Wait limit**: a missing head-of-line sequence number is waited on for at
  most `WaitLimit` ticks, then skipped, so a lost packet never blocks the
  stream forever. Each new gap gets a full wait limit of its own.
- **Cache limit**: at most `CacheLimit` out-of-order packets are buffered;
  exceeding it forces an immediate gap skip and release, keeping memory
  bounded.
- **Late channel**: packets that can no longer be released without modifying
  the already released sequence (old epoch, already released/skipped
  position) go to a separate `Late()` channel. The released sequence is
  never modified retroactively.
- **Epoch reset**: `Reset(epoch, startSeq)` discards the old queue (counted
  in `Stats.Dropped`) so old-epoch packets never leak into the new stream.

## Layout

- `packet.go` — packet type and the `(EventTime, Seq)` ordering key
- `releaser.go` — the reorder releaser (wait limit, cache limit, late
  channel, epoch reset)
- `sim.go` — discrete-event simulator (tick loop, event heap, `Source`,
  `Link`)
- `cmd/demo` — runnable sample scenario
- `releaser_test.go`, `sim_test.go` — `go test` suite

## Run

```sh
go test ./...
go run ./cmd/demo
```
