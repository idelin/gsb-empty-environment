# OR-Set CRDT with Causally-Confirmed Tombstone GC

A single-process, deterministic event simulation of an Observed-Remove Set
(OR-Set) CRDT in pure Go (standard library only, Go 1.11+).

- `orset/` — the CRDT: unique add tags, observed-remove, semilattice merge,
  version-vector-based causal GC.
- `sim/` — the event simulator: virtual clock, message delay/loss/
  duplication/reordering, crash/recover, partition/heal. All communication,
  failures and time are controlled here; the CRDT never touches a wall
  clock or a real network.
- `cmd/demo/` — a scripted run (partition, concurrent add/remove, crash,
  heal, convergence, GC).

## Design

Every mutation is stamped with a unique `Tag{Replica, Seq}`:

- `Add(e)` attaches a fresh unique tag to `e`.
- `Remove(e)` tombstones exactly the tags **currently observed** for `e`
  (observed-remove). A concurrent, not-yet-observed add therefore survives
  (add-wins). A duplicate remove finds no live tags and is a no-op.
- `Merge`/`Join` is a semilattice join: adds and tombstones are set joins
  (adds filtered by the tombstone join; concurrent removes of the same add
  tag keep the minimum remove stamp), version vectors are element-wise max.
- `GC` collects a tombstone only when **every** replica in the roster is
  known — via gossiped version vectors — to have applied the remove
  (causal confirmation). At that point no live copy of the add tag exists
  anywhere, so dropping the tombstone cannot resurrect the element.

## Safety (evidence)

1. **Convergence.** States form a join-semilattice under `Join`
   (commutative, associative, idempotent — `TestMergeCommutative`,
   `TestMergeAssociative`, `TestMergeIdempotent`). Replicas that have
   merged the same set of states are therefore in identical states,
   regardless of message order, duplication or loss
   (`TestConvergenceUnderLossDupReorder`, `TestDeterministicReplay`).
2. **Observed-remove.** `Remove` tombstones only observed tags:
   `TestConcurrentAddSurvivesRemove` (unobserved concurrent add survives),
   `TestDuplicateRemoveIsNoOp` (state unchanged), `TestUniqueTags`
   (concurrent adds never share a tag).
3. **GC cannot resurrect.** A tombstone is collected only after every
   replica's version vector covers the remove, i.e. every replica has
   already killed the add tag; no future merge can reintroduce it
   (`TestCausalGCPreventsResurrection`, `TestGCWaitsForWholeRoster`).
   The necessity of causal confirmation is shown by the counterexample
   `TestPrematureTombstoneGCResurrects`: dropping the tombstone early lets
   a stale replica's merge bring the removed element back.

## Liveness (evidence)

1. **Eventual convergence.** If communication is eventually restored,
   periodic gossip drives all live replicas to the same state in bounded
   rounds after the last mutation (joins are monotonic and the state space
   reachable from a finite op history is finite):
   `TestEventualConvergenceAfterPartition`, `TestCrashRecover`.
2. **GC terminates.** Once the remove has been delivered everywhere and
   version vectors have gossiped back, the confirmation predicate becomes
   true at every replica and tombstones are collected; the element never
   returns (`TestGarbageCollectionLiveness`). Note in-flight messages may
   legitimately re-deliver a tombstone once, so a second GC round collects
   it again — GC is safe to re-run at any time.

## Run

```sh
go run ./cmd/demo
```

Sample output (deterministic, seed 1):

```
[t=007] state  A=[apple banana] B=[apple banana] C=[apple banana]
[t=014] during partition A=[banana] B=[apple banana] C=[apple]
[t=031] healed  A=[apple] B=[apple] C=[apple] converged=true
[t=032] gc A: collected 2 tombstone(s)
[t=032] gc B: collected 2 tombstone(s)
[t=032] gc C: collected 2 tombstone(s)
[t=036] final   A=[apple] B=[apple] C=[apple] converged=true tombstones A=0 B=0 C=0
--- event log ---
[t=004] deliver B -> C
[t=004] deliver C -> A
[t=005] deliver A -> B
... (full delivery/partition/crash/heal trace)
```

Reading the trace: `apple` is removed by A and concurrently re-added by B
during the partition — the unobserved add wins, so `apple` survives;
`banana` is removed once by C (the duplicate remove is a no-op) and stays
removed; after heal + gossip all replicas converge and both tombstones are
garbage-collected only after causal confirmation.

## Test

```sh
go test ./...
```

Result:

```
ok  	orsetdemo/orset
ok  	orsetdemo/sim
```

Test inventory:

| Area | Tests |
|---|---|
| Merge laws | `TestMergeCommutative`, `TestMergeAssociative`, `TestMergeIdempotent` |
| Observed-remove | `TestConcurrentAddSurvivesRemove`, `TestDuplicateRemoveIsNoOp`, `TestUniqueTags` |
| GC safety | `TestPrematureTombstoneGCResurrects` (counterexample), `TestCausalGCPreventsResurrection`, `TestGCWaitsForWholeRoster` |
| Liveness | `TestEventualConvergenceAfterPartition`, `TestConvergenceUnderLossDupReorder`, `TestCrashRecover`, `TestGarbageCollectionLiveness` |
| Simulation | `TestDeterministicReplay` |
