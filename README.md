# Deterministic Event-Replay Debug Kernel

A deterministic event-replay debugging kernel in Go (standard library only).
It records every external input, clock interrupt and random seed as an
append-only event log, and can time-travel to any historical event tick,
reproducing the exact memory state of every component — verified by
byte-identical snapshot hashes.

## Layout

- `replay/event.go` — event types and the append-only `EventLog`
  (seed / packet arrival / timeout clock interrupt).
- `replay/rng.go` — SplitMix64 PRNG; single-`uint64` state, trivially
  snapshotable.
- `replay/system.go` — `NetSystem`, the simulated reliable-transport
  receiver under debug (plain-value state only).
- `replay/kernel.go` — the scheduler: virtual clock, timer queue, event
  application, live `Record`, side-effect-free `ReplayTo` / `TravelTo`.
- `replay/snapshot.go` — deterministic binary serialization + SHA-256
  snapshot hash of clock, RNG, component memory and pending timers.
- `replay/kernel_test.go` — acceptance tests (`go test`).
- `cmd/replaydemo` — runnable end-to-end demo.

## How determinism is guaranteed

1. The first log entry records the random seed; all kernel-internal
   randomness (e.g. simulated ack loss) draws from that seeded RNG only.
2. Every external input (packet arrivals with full payloads) and every
   clock interrupt (retransmission timeouts) is appended to the log.
3. `ReplayTo(log, n)` applies the first `n` events to a pristine kernel —
   no I/O, no shared state — so time travel has zero side effects on the
   live run.
4. Snapshots serialize state in a fixed field order (no maps/pointers),
   so identical logical states hash identically.

## Acceptance boundary

The recorded trace contains exactly 50 packet arrivals and 3 timeouts
(54 events including the seed). Replaying to event #35 yields a state
whose SHA-256 snapshot hash is byte-identical to the hash captured live
at that point.

## Run

```sh
go test ./...        # acceptance tests
go run ./cmd/replaydemo
```

Sample output:

```
recorded 54 events: 50 packet arrivals, 3 timeouts, 1 seed
historical snapshot @ event 35: tick=859 hash=6268ceac6b2074fc
replayed   snapshot @ event 35: tick=859 hash=6268ceac6b2074fc
MATCH: replayed state is byte-identical to the historical snapshot
time-travelled live kernel to event 35 (hash=6268ceac6b2074fc)
returned to present (event 54): live run untouched, hash=f7bb561663644d4a
```
