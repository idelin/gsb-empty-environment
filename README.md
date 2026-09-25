# proofsched — budgeted multi-goal proof search with shared derivations

Python 3.11, standard library only, tested with `unittest`.
All sample data is synthetic (fabricated for demonstration).

## Model

- **Finite derivation graph**: `Fact` nodes (axioms) and `Rule`s
  `premises -> conclusion` over node IDs. Goals are weighted conjunctions of
  node IDs.
- **Stable IDs + versions**: every fact, rule, and derivation step carries a
  stable string ID and an integer version. Evidence (proof certificates)
  records the exact versions it used and can be **replayed** (verified)
  against any later snapshot via `replay_certificate`.
- **Budget**: each rule application costs 1 step. A valid derivation is
  computed **once** and shared by all goals; reuse costs 0
  (`shared_work_avoided`, `duplicate_steps_avoided` statistics).
- **Statuses**: `PROVED` / `DISPROVED` / `UNDECIDED`. A goal is `DISPROVED`
  only when the shared frontier is exhausted (fixpoint reached) — an
  unsearched goal is never called disproved. With budget 0 everything is
  `UNDECIDED`.

## Scheduler

- The engine maintains **one reusable global frontier** (applicable but
  unapplied rules), shared across goals — never recomputed per goal.
- **Exact reference** (`exact.py`): exhaustive search for the maximum proved
  goal weight within budget; exponential, for small instances only.
- **Heuristic** (`scheduler.py`): greedy goal-weight prioritisation for
  larger instances. It does **not** claim optimality; the demo prints both.

## Retraction, versions, budget recovery

- `retract_fact` / fact or rule **version bumps** invalidate dependent
  derivations, **recover** the budget they consumed, and force
  **re-verification** of stale frontier entries that referenced the dead
  evidence (`frontier_revalidations`).
- Conclusions with an already-computed **alternative proof** are re-attached
  for free (`alternative_proofs_reused`).
- `add_budget` never revokes verified conclusions; proved weight is monotone
  non-decreasing in budget.

## Run

```sh
python3.11 run_sample.py                 # demo: sample world + all invariants
python3.11 -m unittest discover -s tests -v
```
