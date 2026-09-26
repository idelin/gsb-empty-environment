# Budgeted Project Selection (预算内项目选择)

Select a subset of projects maximizing total profit under a budget, with
dependency, mutual-exclusion, and shared-cost semantics. Pure Python 3.11
standard library; **no external solver** — exact branch-and-bound over
strongly connected components. All data is synthetic integers or rationals
(`fractions.Fraction`), and all arithmetic is exact.

## Semantics (规则)

- **依赖必选**: selecting a project requires selecting all of its
  dependencies, transitively. Selecting a child without its parent is
  illegal (`check_selection` reports it).
- **共享依赖只付一次**: two selected projects sharing a dependency pay its
  cost exactly once (cost is summed over the *set* of selected projects).
- **互斥**: mutex pairs may never be selected together; a mutex *inside* a
  dependency cycle makes that cycle-group unselectable.
- **负收益依赖**: a dependency with negative profit is selected exactly when
  a profitable dependent pulls it in.
- **依赖环 (cycles)**: handled explicitly via `on_cycle`:
  - `"group"` (default): the whole strongly connected component becomes one
    atomic group — select all or none, cost/profit summed;
  - `"reject"`: the instance is refused with status `rejected_cycle` and
    the cycle members are reported.

## Result categories (结果分类，彼此分开)

- **可行 (feasible)**: `is_feasible_selection(instance, pids)` /
  `check_selection` validate any candidate set (closure, mutex, required,
  budget).
- **最优 (optimal)**: `solve(instance)` returns `status="optimal"` with the
  exact best selection; `status="infeasible"` when no feasible selection
  exists (e.g. required projects conflict or exceed the budget).
- **预算未决 (budget undecided)**: `budget_binding is False` means the same
  optimum is achievable with no budget limit at all — leftover budget did
  not decide the outcome; `budget_binding is True` means the budget was
  decisive.

## Usage

```python
from project_select import Instance, Project, solve

inst = Instance(
    projects=[
        Project("core-lib", 3, -1),                 # shared, negative profit
        Project("app-a", 2, 6, deps=["core-lib"]),
        Project("app-b", 2, 5, deps=["core-lib"]),
    ],
    mutex=[],
    budget=8,
)
res = solve(inst)            # or solve(inst, on_cycle="reject")
res.selected                 # frozenset({'app-a', 'app-b', 'core-lib'})
res.total_cost               # 7  (core-lib paid once)
res.total_profit             # 10
res.budget_binding           # True
```

## Layout

- `project_select/model.py` — `Project`, `Instance` (validation, exact numbers)
- `project_select/solver.py` — Tarjan SCC, cycle policies, feasibility
  checks, exact branch-and-bound solver
- `examples/demo.py` — runnable sample (5 scenarios)
- `tests/test_solver.py` — unittest suite, incl. randomized equivalence
  against an independent brute-force enumerator

## Run

```
python3 examples/demo.py          # sample run
python3 -m unittest discover -s tests -v
```

## Sample run (excerpt)

```
--- optimal under budget 8 ---
  status           : optimal
  selected         : ['app-a', 'app-b', 'core-lib']
  total cost       : 7  (budget 8, remaining 1)
  total profit     : 10
  budget role      : decisive (budget binding)
  feasibility check: OK

--- on_cycle='reject' (instance refused, cycle reported) ---
  status           : rejected_cycle
  reason           : dependency cycle rejected: ['auth', 'session']

--- required 'must' costs 10 > budget 5 ---
  status           : infeasible
```

## Test results

```
$ python3 -m unittest discover -s tests
.............................................................
Ran 21 tests in ~0.2s
OK
```

Includes 700 randomized instances (integer / rational / required /
cycle-bearing) cross-checked against independent subset enumeration.
