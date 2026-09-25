# Multi-table Join Planner with Adaptive Execution (Python 3.11, stdlib only)

A from-scratch implementation of a join-order planner and executor over
local synthetic relations. No SQL engine is invoked: joins are evaluated
directly as multiset (bag) operations.

## Layout

- `joinplanner/relations.py` — bag relations, NULL semantics, in-memory
  hash join and hash-partitioned (spill) join, both for `INNER` and
  `LEFT OUTER`.
- `joinplanner/stats.py` — interval statistics (`Interval[lo, hi]`);
  unknown stats are wide intervals; `StatsProvider` carries an epoch for
  stale-stat detection.
- `joinplanner/cost.py` — cost = build/probe/output row cost + resident
  hash-table memory pressure + spill (write/read) cost when the build side
  exceeds the memory budget. Deliberately not a single selectivity product.
- `joinplanner/planner.py` — exhaustive enumeration of *legal* orders and
  interval-aware costing. INNER steps reorder freely; a LEFT OUTER step is a
  barrier (it can only be placed once exactly its original prefix is
  present); `requires` models correlated dependencies. The primary plan
  minimizes pessimistic (worst-case) cost; conservative alternatives are
  returned with it.
- `joinplanner/executor.py` — materializes every stage, so stage boundaries
  are safe switch points. Rechecks stat epochs and compares observed vs
  expected intermediate rows; replans the remainder (the materialized
  intermediate becomes the new base with exact measured stats) only at a
  boundary. Rows are emitted only from the final stage in chunks, so
  replanning can never repeat rows and the final multiset is invariant.
  Terminal status: `COMPLETED` / `BUDGET_EXHAUSTED` / `CANCELLED`; flags:
  `STALE_STATS`, `REPLANNED`, `SPILLED`. Every decision is logged with its
  numeric inputs (observed rows, estimate, costs, chosen/alternative
  orders), making each decision reproducible.
- `joinplanner/samples.py` — deterministic, replayable scenarios.
- `run_samples.py` — prints every scenario's decision records as JSON.
- `tests/` — unittest suite.

## Semantics

- Relations are bags: duplicates count and equijoins multiply
  multiplicities. Results of reordered plans are compared with
  `Relation.canonical_multiset()` (keyed by logical
  `(owning relation, column)` lineage), which ignores physical column
  ordering.
- `NULL` never equals anything (including NULL); NULL build keys never
  match; unmatched left rows of a LEFT OUTER join are padded with NULLs.
- The partitioned path hashes both sides by a process-stable key hash
  (`zlib.crc32(repr(k))`) and joins per partition; its bag output is
  asserted equal to the in-memory path.

## Scenarios

1. `low_selectivity_exception` — three-table triangle where the
   lowest-first-stage-selectivity (greedy) order is ~11x worse; exhaustive
   execution of all legal orders on the real data verifies the planner is
   optimal (gap 1.0).
2. `outer_join_barrier` — `(R LEFT JOIN S) INNER JOIN T` vs the illegal
   reassociation `R LEFT JOIN (S INNER JOIN T)` produce different bags
   (0 rows vs 1 padded row); exactly one legal order exists.
3. `duplicate_key_spill` — duplicate keys produce a 250k-row intermediate
   beyond memory; the partitioned path is selected and matches the
   in-memory bag.
4. `adaptive_replan` — underestimated base table + stale statistics for
   another table; the plan switches at materialization boundaries
   (`[A,B,C,D]` -> executed `[D,B,C]`), one stage partitions, final bag
   equals the reference evaluation and emitted rows equal the result.
5. `estimate_deviation` — correlated dependency fixes the first table; a
   ~500x estimate deviation flips the remaining tail `[B,C,D]` ->
   `[B,D,C]`; bag invariant and no duplicate emission.
6. `terminal_states` — stale stats (`STALE_STATS`), exhausted budget
   (`BUDGET_EXHAUSTED`) and cancellation (`CANCELLED`, with the emitted
   prefix a sub-bag of the final result).

## Run

```bash
python3.11 -m unittest discover -s tests -t . -v
python3.11 run_samples.py
```
