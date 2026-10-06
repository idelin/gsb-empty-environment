# eventsched

A forkable, persistent priority queue for event scheduling, in Go.

A `Branch` is an immutable snapshot of a schedule. Every mutating operation
returns a new `Branch` and leaves the receiver untouched, so forking is free
and branches never observe each other's updates or cancellations.

## Semantics

- Events are ordered by `Time`, then by `ID`; `Peek`/`Pop` return the minimum.
- `Upsert` replaces any previous event with the same ID. The old queue entry
  is invalidated (per-ID revision numbers) and discarded lazily.
- `Cancel` removes an ID from the current branch only; sibling branches
  forked from the same ancestor keep their copy.
- `Merge` combines two branches: for each ID the entry with the larger
  revision wins; equal revisions with different content yield a
  `*ConflictError`. IDs present on only one side are kept (so merging a
  cancelled branch with one that still has the event brings it back).

## Implementation

- Persistent AVL tree (path-copying) maps event ID → entry{event, revision}.
- Persistent leftist heap ordered by (time, id, revision) serves as the
  queue; entries whose revision/content no longer matches the dictionary are
  stale and skipped lazily by `Peek`/`Pop`.
- `Merge` rebuilds the queue from the surviving entries instead of melding
  the two input heaps: branches share heap structure with their ancestors,
  and melding would give shared stale entries exponential path multiplicity.

## Testing

- `schedule_test.go` — unit tests for ordering, stale-entry invalidation,
  cancel isolation, fork independence, and merge rules.
- `model_test.go` — randomized differential test against a reference model
  (a plain versioned dictionary per branch, answers computed by sorting),
  driving fork/upsert/cancel/peek/pop/merge sequences over a branch pool.

```sh
go test ./...
```
