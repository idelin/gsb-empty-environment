# expsched

Experiment-selection solvers with device cooldowns and mutual-exclusion
groups, plus a two-stage observation-based variant.

## Model

- Each candidate experiment occupies one integer time slot on one device;
  its profit may be negative (the empty subset is always feasible).
- Cooldown `c`: two experiments on the same device must leave at least `c`
  full slots between them, i.e. their slots differ by at least `c+1`.
- Mutex group (`group` field): at most one member per group may be selected.
- Tie-breaks: higher profit, then fewer experiments, then the
  lexicographically smaller sorted id list.

## Stage 1: single-stage selection (up to 20 candidates)

`sched.Solve` is a branch-and-bound over slot-ordered candidates with
incremental legality checks. It is cross-checked two ways:

- `sched.BruteForce` enumerates all 2^n subsets (`cmd/stage1 -verify`).
- `sched.ReplayLegal` replays the chosen set on the timeline in slot order,
  enforcing per-device cooldown and mutex rules.

```sh
go run ./cmd/stage1 -verify examples/stage1.json
```

## Stage 2: two-stage plan with observations (up to 6 scenarios, 10 candidates)

Candidates with `slot <= stage1_end` are stage-1 eligible, the rest stage-2
eligible; cooldown and mutex rules apply to the union of both stages. Each
scenario defines per-candidate profits and the observation value a candidate
reveals when run in stage 1. The stage-1 set therefore determines which
scenarios become distinguishable; stage-2 branches may depend only on the
observed group.

`sched.SolveTwoStage` enumerates legal stage-1 sets, partitions scenarios by
observation vector, and per observation group picks the stage-2 set
maximizing the worst in-group profit. The strategy tree maximizes the
worst-case total profit.

Verification (`cmd/stage2 -verify`):

- `sched.VerifyStrategy` replays every scenario path: eligibility,
  disjointness, combined-timeline legality, and causality — routing is a
  pure function of the observation key, so scenarios with identical
  observations are forced onto the same branch (no clairvoyance); the
  claimed worst-case profit must match the replayed one exactly.
- `sched.BruteTwoStage` independently re-enumerates the optimum;
  `sched.ClairvoyantBound` gives the oracle upper bound.

```sh
go run ./cmd/stage2 -verify examples/stage2.json
```

## Tests

```sh
go test ./sched/
```

Randomized cross-checks (solver vs. exhaustive enumeration; strategy vs.
naive enumeration and the clairvoyant bound) plus hand-crafted boundary
cases for cooldown, mutex, tie-breaks, and observational value.

Note: built with Go 1.11; set `GOCACHE` to a writable dir if the default
cache is unavailable, e.g. `GOCACHE=/tmp/gocache go test ./sched/`.
