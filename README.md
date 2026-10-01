# Budgeted Maximum Coverage (weighted, with dependencies / conflicts / min-cover)

Pure **Python 3.11**, standard library only (`fractions`, `json`, `hashlib`,
`unittest`, ...). No external solver is called. All constraint data are
synthetic integers or rationals and are handled exactly as
`fractions.Fraction`.

## Problem

Choose sets whose total cost fits the budget to maximise covered weight:

- each element pays its weight **exactly once**, no matter how often covered;
- an element pays only when covered at least `min_cover[element]` times
  (default 1);
- selecting a set forces its `requires` closure to be selected too;
- sets in `excludes` are mutually exclusive (checked both directions);
- zero-cost sets are handled first and never divide by zero; empty sets add
  no profit.

## Layout

| File | Role |
| --- | --- |
| `budgeted_coverage/model.py` | `Instance`, `CoverageSet`, exact evaluation, `remove_set`, fingerprint |
| `budgeted_coverage/greedy.py` | greedy heuristic (status `FEASIBLE` only) |
| `budgeted_coverage/exact.py` | full subset enumeration reference (status `OPTIMAL`) |
| `budgeted_coverage/bnb.py` | resumable branch-and-bound with per-prune bound justifications |
| `budgeted_coverage/witness.py` | witnesses + re-validation after dynamic changes |
| `budgeted_coverage/report.py` | heuristic-vs-exact gap report (no approximation guarantee claimed) |
| `main.py` | runnable demo |
| `tests/test_budgeted_coverage.py` | 23 unittests |

## Statuses (kept separate)

- `OPTIMAL` — proven by exhaustive enumeration or completed branch-and-bound;
- `FEASIBLE` — feasible incumbent only (e.g. greedy), no optimality claim;
- `BUDGET_UNDECIDED` — search budget (node limit) exhausted; the result
  carries a feasible incumbent **and** a reliable upper bound, the gap is
  undecided.

## Branch-and-bound bound (why pruning is sound)

Node bound = `current profit + optimistic remainder`, where each remaining
set contributes an *overestimated* marginal (uncovered elements may be
counted by several sets; min-cover thresholds ignored) packed into the
remaining budget as a fractional knapsack; zero-cost sets add their full
optimistic marginal for free. The bound is capped by the total weight of
not-yet-satisfied elements. Both terms overestimate any reachable profit,
so `bound <= incumbent` is a valid prune — and every prune is logged with
its numeric reason (`bound` / `budget` / `conflict` / `dependency-*`).

## Resume & dynamic changes

`BranchAndBound.save_checkpoint(path)` serialises the full DFS state;
`BranchAndBound.load(instance, path)` resumes it. Checkpoints and witnesses
are bound to the instance fingerprint: after `instance.remove_set(...)` the
old witness fails `verify_witness` and the old checkpoint raises
`CheckpointMismatch` — stale results are never reused, the instance is
re-solved.

## Run

```
python3.11 main.py                          # demo
python3.11 -m unittest discover -s tests -v # tests
```

## Sample run (`python3.11 main.py`)

```
== 1. Integer instance with dependencies, conflicts and min-cover ==
  greedy                       status=feasible         profit=10  cost=2 (budget 5)  selected=[free_boost, rival]
  exact enumeration            status=optimal          profit=16  cost=5 (budget 5)  selected=[core, pair1, pair2]
  branch-and-bound             status=optimal          profit=16  cost=5 (budget 5)  selected=[core, free_boost, pair1, pair2]
  b&b explored 12 nodes, 13 prunes
  heuristic=10 reference=16 abs_gap=6 rel_gap=3/8
  note: Gap reported descriptively only; no approximation guarantee is claimed or implied for the heuristic on this problem variant.

== 2. Rational (fractional) costs and weights ==
  greedy                       status=feasible         profit=19/3  cost=5/4 (budget 3/2)  selected=[s1, s2]
  exact enumeration            status=optimal          profit=19/3  cost=5/4 (budget 3/2)  selected=[s1, s2]
  heuristic=19/3 reference=19/3 abs_gap=0 rel_gap=0
  note: Gap reported descriptively only; no approximation guarantee is claimed or implied for the heuristic on this problem variant.

== 3. Resumable branch-and-bound with prune justifications ==
  after 3 nodes: status=budget_undecided incumbent=15 reliable_upper_bound=32
  resumed to completion: status=optimal profit=29 selected=[freebie, set01, set03, set08, set10]
  total nodes=53, prunes=54; sample reasons:
    [conflict] depth=4: including set02 creates mutually exclusive pairs [(set02, set03)]
    [bound] depth=7: subtree upper bound 27 <= incumbent 27
    [budget] depth=7: including set09 costs 12 > budget 8
    [dependency-excluded] depth=9: including set05 forces previously excluded sets [set06]

== 4. Dynamic set deletion invalidates old witnesses ==
  witness valid before change: True
  removed set core; old witness re-validates: False
    - instance changed: fingerprint c4bf83979c5392eb != dfdae3387589ed34
    - witness selects sets no longer present: [core]
  checkpoint resume refused: checkpoint fingerprint c4bf83979c5392eb != instance dfdae3387589ed34; the instance changed, re-solve from scratch
  re-solved after deletion     status=optimal          profit=16  cost=4 (budget 5)  selected=[free_boost, pair1, pair2, rival]
```

## Test results (`python3.11 -m unittest discover -s tests`)

```
Ran 23 tests in ~3s

OK
```

Coverage of the suite: profit-counts-once and min-cover thresholds, empty /
zero-cost sets (no division by zero), dependency / conflict / budget
violations, exact rational arithmetic, greedy never beats exact and never
claims optimality, branch-and-bound == brute force on 40 seeded random
instances, interrupted runs still bound the optimum from above, prune
records carry reasons, checkpoint resume == uninterrupted run, checkpoint
rejected after instance change, witness re-validation after `remove_set`,
gap report values.
