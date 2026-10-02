# Deterministic Event-Replay Debugging Kernel

A deterministic event-replay debugging kernel in Go (standard library only).
It records every external input, clock interrupt and the random seed as an
append-only event log, and supports side-effect-free time travel: jumping to
any historical event tick reproduces the exact memory invariants of all
components at that moment.

## Design

- `event.go` — event types and the append-only `EventLog` (seed + ordered events).
- `scheduler.go` — deterministic scheduler: min-heap ordered by
  `(tick, kind, seq)`, with timer cancellation mirroring a real kernel.
- `system.go` — the simulated machine: PRNG, NIC receive queue,
  retransmission timer table and statistics. Packet loss is decided by PRNG
  draws, so the recorded seed fully determines the loss pattern.
- `prng.go` — splitmix64 PRNG with a single-`uint64` serializable state
  (used instead of `math/rand`, whose state cannot be snapshotted).
- `snapshot.go` — canonical little-endian binary encoding of the whole
  kernel state (clock, pending scheduler events, PRNG, stats, queues,
  timer table) plus its SHA-256 hash.
- `kernel.go` — the recording `Kernel` (log + per-event snapshots) and the
  `Debugger` providing time travel.

## How time travel works

`Debugger.Goto(index)` never touches the recording kernel. It builds a
fresh kernel from the recorded seed, re-submits only the *external* events
(packet arrivals, clock interrupts) from the log, and lets the scheduler
re-derive every internal timeout. It then verifies that the regenerated
event sequence matches the recorded log prefix exactly — proving the run
is deterministic — and returns the kernel positioned right after the
requested event.

## Acceptance boundary

The reference trace (`BuildTrace` + `TraceSeed`) contains **50 packet
arrivals and exactly 3 timeouts** (plus 7 clock interrupts, 60 events in
total). Replaying to the **35th event** and comparing the reconstructed
state with the historical snapshot hash must match byte-for-byte.

## Run

```sh
go test ./... -v   # run the test suite
go run ./cmd/demo  # run the record + time-travel demo
```

## Sample run

```
=== deterministic event replay debugging kernel ===
seed: 2
recorded 60 events: 50 packet arrivals, 3 timeouts, 7 clock interrupts
final stats: {Arrivals:50 Processed:48 Timeouts:3 ClockInterrupt:7 BytesProcessed:14310 Retransmits:3}

event log (excerpt):
  [00] tick=1   PacketArrival  id=1000 size=64
  [01] tick=2   PacketArrival  id=1001 size=101
  ...
  [54] tick=48  Timeout        timer=1017
  [57] tick=56  ClockInterrupt
  [58] tick=75  Timeout        timer=1044
  [59] tick=78  Timeout        timer=1047

=== time travel to event 35 ===
event:                tick=32  PacketArrival  id=1031 size=187
historical snapshot:  aca48e547d14338a1944402bc1654abccf6bf6a6ac28454710dc7a2d4a674ad3
reconstructed state:  aca48e547d14338a1944402bc1654abccf6bf6a6ac28454710dc7a2d4a674ad3
hash identical:       true
bytes identical:      true (1868 bytes compared)

full sweep: 60/60 checkpoints reproduced byte-for-byte, 0 mismatches
```

## Test results

```
=== RUN   TestAcceptanceReplayToEvent35
    kernel_test.go:28: recorded trace: 60 events (50 arrivals, 3 timeouts, 7 clock interrupts), seed=2
    kernel_test.go:49: event 35: tick=32  PacketArrival  id=1031 size=187
    kernel_test.go:50: historical snapshot hash: aca48e547d14338a1944402bc1654abccf6bf6a6ac28454710dc7a2d4a674ad3
    kernel_test.go:51: replayed   state   hash: aca48e547d14338a1944402bc1654abccf6bf6a6ac28454710dc7a2d4a674ad3
--- PASS: TestAcceptanceReplayToEvent35 (0.00s)
=== RUN   TestTimeTravelEveryCheckpoint
--- PASS: TestTimeTravelEveryCheckpoint (0.01s)
=== RUN   TestTimeTravelIsSideEffectFree
--- PASS: TestTimeTravelIsSideEffectFree (0.00s)
=== RUN   TestReplayReDerivesInternalEvents
--- PASS: TestReplayReDerivesInternalEvents (0.00s)
=== RUN   TestGotoOutOfRange
--- PASS: TestGotoOutOfRange (0.00s)
PASS
ok  	replaykernel	0.306s
```
