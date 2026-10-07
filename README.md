# evsched

A Go event-scheduling library built around a **forkable, versioned priority
queue**. Events are dequeued in `(time, id)` order; rescheduling an event
invalidates its old entry via per-id revision numbers.

## Semantics

- `Scheduler.NewQueue()` creates an empty version; `q.Fork()` branches it.
  Branches are fully independent: mutating one never affects another.
- `q.Upsert(id, time, data)` schedules or reschedules an event. The id's
  revision number increments and the stale heap entry is skipped lazily on
  `Peek`/`Pop`.
- `q.Cancel(id)` removes the event from the current branch only. The id's
  revision counter is retained (not rewound), so a later re-add counts as
  newer than anything visible before the cancel.
- `Merge(a, b)` combines two branches of the same scheduler:
  - same id in both: the entry with the **larger revision** wins;
  - same id, **same revision, different content**: `*ConflictError`;
  - id in only one branch: carried over.
- `q.Snapshot()` returns the version's live events sorted by `(time, id)`.

## Verification

`model_test.go` checks the implementation against a reference **versioned
dictionary model**: every version is an immutable snapshot of
`id -> (revision, time, data)` plus revision counters that survive
cancellation, and the observable state is the dictionary sorted by
`(time, id)`. A randomized op sequence (fork / upsert / cancel / pop / merge,
multiple seeds) is replayed against both, and every live handle's snapshot,
length, and `Peek` result is compared after each step — covering stale-entry
invalidation and merge conflict behavior.

## Test

```sh
go test ./...
```
