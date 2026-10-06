# agglog

Aggregate log in Go: runs of same-key increments are compressed into
batches, and batches are applied idempotently against a per-event set
model.

## Semantics

- `Pack` compresses a run of same-key `Event`s into a `Batch` that
  lists the original event ids and each increment's delta, preserving
  order. `Unpack` expands a batch back into its events.
- `Log` keeps per-key totals plus the set of applied event ids
  (id -> delta).
- `ApplyBatch` applies only unseen entries, in listed order:
  - an already-applied id with the same delta is skipped (replay-safe);
  - an already-applied id with a different delta — or the same id with
    two different deltas inside one batch — rejects the whole batch
    with a `ConflictError` before anything is applied (atomic).
- `ApplyEvent` applies single events with the same replay/conflict
  rules.

## Model verification

`agglog_test.go` checks the batch machinery against a reference
per-event set model that applies events one at a time:

- batch overlap: partially processed batches apply only unseen events;
- replay: re-applying any earlier batch is a no-op;
- unpack order: batch application equals ordered per-event application,
  and disjoint batches converge to the same state in any arrival order.

Run: `go test ./...`
