# Replay Window Handoff

```go
import "prescreen.local/ddd-2011-20261122-v2/replayhandoff"
```

`Receiver` partitions replay state by persistent device identity and key
generation. Wire sequence numbers use 4 through 32 low bits. Signed
checkpoints provide full sequence anchors, and each node log preserves the
strict order in which that node observed authenticated receipts.

`Admit` performs verification, constraint solving, replay classification, and
registration under one per-state lock. A frame is accepted only when every
compatible full-sequence assignment is fresh. If every compatible assignment is
old it is rejected; mixed old/fresh assignments return `OutcomeNeedAnchor` with
both complete assignments and the low sequence that needs another checkpoint.
Hard state conflicts return `OutcomeConflict` separately. Invalid
authentication returns before any state lookup or mutation.

The small-bit-width tests include an independent exhaustive assignment
interpreter. It enumerates high-bit choices rather than calling the production
solver, and checks two wraps, half-window boundaries, distinct node orders,
parallel key generations, and concurrent arrivals of the same frame.
