# rescheck

A bounded model checker for resource-release bugs across concurrent,
finite action scripts.

## Model

The input is a single JSON file (see `examples/`) describing:

- **Resource state machine**: named resources, an initial valuation, and
  per-resource *terminal* states (the states in which a resource counts as
  released).
- **Actions**: each action has one or more public transitions
  `{outcome, when, set}`. Failure is modeled explicitly: an action that
  cannot succeed still has an observable transition (e.g. `acquire` with
  outcome `fail` when the lock is held). Every transition fires only when
  its `when` guard matches the current resource valuation.
- **Scripts**: named, finite, ordered lists of action names.
- **Budget**: `max_states` and `max_depth` bounds for exploration.

## Checking

`Check` (`explore.go`) enumerates all interleavings of the scripts that
preserve each script's internal order, using BFS over composite states
(resource valuation + per-script program counters). Both success and
failure transitions branch the search. A composite state is terminal when
every script is exhausted; a terminal state with any resource outside its
terminal states is a counterexample. BFS order guarantees the reported
trace is a shortest one.

Verdicts:

- `violation` — counterexample found; the trace and unreleased resources
  are reported.
- `safe` — exploration completed; every terminal interleaving releases
  all resources.
- `unknown` — a budget was exhausted before a verdict could be reached
  (the `reason` field says which budget).

## Independent replay verification

`Replay` (`replay.go`) is a separate implementation that re-executes a
reported trace from the initial state, re-validating script order, action
existence, outcome availability, guards, and completeness (all scripts
must run to the end). `VerifyCounterexample` additionally requires the
replayed final state to actually have unreleased resources, so a buggy
explorer cannot fabricate a counterexample.

## Usage

```sh
go build -o rescheck .
./rescheck examples/leak.json   # violation, replay_verified: true, exit 10
./rescheck examples/safe.json   # safe, exit 0
# unknown exits 11; pass -verify=false to skip replay verification
go test ./...
```
