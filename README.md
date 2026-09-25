# Temporal Evidence Selection

Selects, from multi-version facts, evidence sets that are simultaneously
visible at one observation time and whose business validity intervals
overlap, subject to source mutual-exclusion rules. The solver returns all
maximal feasible time segments with at least one consistent witness
selection each, or explicit inconsistency reasons. Pure Python 3.11
standard library; tests use `unittest`. All sample data is synthetic.

## Model

- `FactVersion(fact_id, version, dimension, source, valid_from, valid_to,
  recorded_at, value)` — one version of a fact. Business validity is the
  half-open interval `[valid_from, valid_to)`; `recorded_at` is the
  transaction time. Stable identity: `fact_id` + `version`.
- `MutexRule(rule_id, version, sources)` — facts from two distinct sources
  in `sources` may never be selected together. Stable identity:
  `rule_id` + `version`.
- `Query(query_id, observation_time, dimensions)` — a complete selection
  picks exactly one visible fact per required dimension.

Visibility is bi-temporal: for each `fact_id` only the latest version with
`recorded_at <= observation_time` is visible. Evidence recorded after the
observation time never participates in the historical explanation; ranges
that only such late evidence could cover are reported as `NOT_VISIBLE`.

## Solver semantics

A time point is feasible iff some complete, mutex-consistent selection has
a common business interval covering it. The solver sweeps atomic segments
between interval endpoints (coverage is constant inside each), evaluates
feasibility per segment, and merges adjacent segments with equal status
into maximal segments. It never just intersects all candidates: per
dimension alternatives are enumerated, and a merged feasible segment may
be witnessed by different selections on different sub-ranges
(`covers_entire_segment=False` flags that case).

Status and reason codes:

- `feasible` — maximal common interval; `witnesses` holds at least one
  consistent selection.
- `infeasible` — with reasons `NO_COVERAGE`, `NOT_VISIBLE`, or
  `MUTEX_CONFLICT` (detail names the conflicting sources and rule refs).
  Touching intervals such as `[0,5)` and `[5,9)` have an empty
  intersection (`EMPTY_INTERSECTION` from `common_interval`), so a pair
  like that yields only `NO_COVERAGE` segments.
- `unknown` — search budget (`max_atomic_segments` or
  `max_combinations`) was exhausted; the unfinished range is kept
  explicitly with reason `SEARCH_INCOMPLETE`.

Every run records a journal: steps `step-0001`, `step-0002`, ... each with
a stable ID, a version, a kind (`visibility`, `atomic`, `segments`,
`update`), and a payload naming the exact fact/rule refs used. Segment IDs
(`seg[lo~hi)#status`) are deterministic functions of the inputs, so
evidence is fully replayable: `replay_solve(facts, rules, query, result)`
re-executes and compares both segments and every journal step.

## Incremental updates

`IncrementalEngine.apply_update(new_version)` computes the affected time
range from the old and new visible versions, aligns it to existing atomic
boundaries, undoes only the segments overlapping that range, recomputes
just those atomic parts, and re-merges with the kept neighbours. Updates
invisible at the observation time are no-ops. `engine.replay()` re-runs
the recorded load plus updates and verifies identical results.

## Oracle cross-check

`evidence.bruteforce.exhaustive_feasible_intervals` enumerates every
complete selection and unions the common intervals of the consistent ones.
`tests/test_bruteforce_crosscheck.py` compares the sweep solver and the
incremental engine against this exhaustive oracle on hundreds of seeded
random small models.

## Layout

- `evidence/model.py` — facts, rules, queries, visibility, interval math
- `evidence/solver.py` — sweep-line solver, reasons, witnesses, merging
- `evidence/bruteforce.py` — exhaustive-selection oracle
- `evidence/incremental.py` — incremental undo/recompute engine
- `evidence/journal.py`, `evidence/replay.py` — evidence journal, replay
- `examples/sample.py` — runnable synthetic sample of every feature
- `tests/` — unittest suite

## Run

```sh
python3.11 examples/sample.py
python3.11 -m unittest discover -s tests -t . -v
```
