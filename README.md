# ormap — observed-remove multi-value map (Go)

A CRDT map where each key holds every concurrently-live assignment.

## Model

- Every `Set` mints a unique `Dot{Replica, Counter}` from the replica's
  logical clock; the dot identifies the assignment event forever.
- `Remove(key)` carries exactly the dots of `key` the remover had observed
  at call time and deletes only those. Concurrent assignments whose dots
  were not observed survive.
- `Live(key)` returns all surviving assignments. `Display(key)` resolves a
  single presentation value by highest logical time, ties broken by
  replica ID, and reports a conflict flag; it never prunes other values.
- Tombstones live in a `CausalContext`: a version vector for each
  replica's contiguous dot prefix plus individual out-of-order dots.
  Compaction folds dots into the vector as gaps fill, preserving exactly
  the causal information needed to stop re-delivered old dots from
  resurrecting removed values.

## Merging

Operations are delta-states (`type Op = State`), so one `Merge` covers
op-based and state-based dissemination as well as op merging. Merge is
commutative, associative and idempotent; duplicate delivery is a no-op.
Merging two states that assign different values to the same dot fails
with `DotConflictError`.

## Checkpoints

`State.Export()` / `ormap.Import()` serialize and restore the full state
(live store + compacted causal context). After a restore, replaying stale
messages cannot resurrect removed values.

## Layout

- `dot.go` — `Dot`, `CausalContext` (compacting tombstones)
- `state.go` — `State`/`Op`, merge, `Live`, `Display`
- `replica.go` — `Replica`: `Set`, `Remove`, `Apply`, `MergeState`
- `checkpoint.go` — `Export` / `Import`
- `ormap_test.go`, `reference_test.go` — reference model as direct op-set
  interpretation; exhaustive 3-replica delivery-order + duplicate tests
- `cmd/demo` — runnable synthetic-event sample

## Run

```sh
go test ./...
go run ./cmd/demo
```

(On Go 1.11 set `GO111MODULE=on`; if the build cache is not writable,
point `GOCACHE` at a writable directory.)
