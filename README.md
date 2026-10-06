# simlib — deterministic entity simulation

A small Go simulation library built around reproducibility:

- **Per-entity random state** — each entity owns a private splitmix64
  stream seeded from `(engine seed, entity ID)`. Adding or removing
  unrelated entities never perturbs existing entities' sequences.
- **Explicit event order** — events execute in `(Time, Seq)` order; the
  pair must be unique and not in the past, so ordering is decided
  entirely by the caller.
- **Versioned checkpoints** — `Engine.Save` snapshots the clock, all
  entity state (including RNG state and draw counts), and the pending
  queue. `Load` rejects any checkpoint whose `version` does not match
  `CheckpointVersion`.
- **Replay comparison** — `Diff(a, b)` compares two engines field by
  field (state, RNG state, per-entity rand call counts, queue), so two
  branches forked from one checkpoint can be verified to replay
  identically.

```go
eng := sim.NewEngine(42, handler)
eng.AddEntity("alice")
eng.Schedule(sim.Event{Time: 1, Seq: 1, EntityID: "alice", Kind: "accrue", Amount: 5})
eng.Run()

blob, _ := eng.Save()
branchA, _ := sim.Load(blob, handler)
branchB, _ := sim.Load(blob, handler)
// ... feed both the same events ...
diffs := sim.Diff(branchA, branchB) // empty == identical replay
```

Run tests with `go test ./...`.
