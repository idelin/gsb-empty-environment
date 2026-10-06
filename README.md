# dedupstore

A Go deduplication result store. Subsequent operations reference prior
results instead of re-executing business logic; garbage collection is
governed by a reference ledger so cleanup can never trigger re-execution.

## Lifecycle model

Two entity kinds with deliberately different lifecycles:

- **Result entity** — the recorded outcome of an executed operation.
  Evicted only when *both* conditions hold: its retention period has
  expired **and** it has no active references (`Acquire`/`Release`).
- **Anti-duplicate tombstone** — a lightweight marker created alongside the
  result. It outlives the result entity and is removed only after the
  source watermark is confirmed past the record's source position
  (`AdvanceWatermark`).

`Resolve(key)` therefore has three outcomes:

| State           | Meaning                                             |
|-----------------|-----------------------------------------------------|
| `StateLive`     | Result present — reuse it, do not re-execute.       |
| `StateDuplicate`| Result collected, tombstone present — already processed, do not re-execute. |
| `StateUnknown`  | Neither present — the business operation must run.  |

`StateUnknown` is only reachable after watermark confirmation, so a
re-execution is always legitimate.

## Reference ledger

Every acquire, release, and eviction is written to a `Ledger`
(引用账). `Ledger.Verify()` audits the trail and proves cleanup safety:

- no result was evicted with outstanding references;
- no tombstone was evicted while references were outstanding;
- no key's reference balance went negative or was stranded by eviction.

`TestCleanupNeverTriggersReexecution` hammers the store with concurrent
acquirers and sweepers and asserts the ledger verifies clean.

## Usage

```go
s := dedup.NewStore(time.Hour)

switch st, v := s.Resolve("order-123"); st {
case dedup.StateLive, dedup.StateDuplicate:
    // already processed — reuse / skip
    _ = v
case dedup.StateUnknown:
    receipt := executeBusinessOp()
    s.Record("order-123", receipt, sourceSeq)
}

h, _ := s.Acquire("order-123") // pin the result while in use
defer h.Release()

s.AdvanceWatermark(confirmedSeq) // confirm source progress
s.Sweep()                        // collect expired, unreferenced entities

if err := s.Ledger().Verify(); err != nil {
    log.Fatalf("unsafe cleanup: %v", err)
}
```

## Test

```sh
go test ./dedup -race -count=1
```
