# Single-machine scheduling: release times, deadlines, type setups

Exact branch-and-bound minimization of **total tardiness** on one machine.
Python 3.11, standard library only, no external solvers.

## Model

- Each `Job` has `release`, `processing`, `deadline`, `job_type`
  (`int` or `fractions.Fraction` — synthetic integer/rational data, exact
  arithmetic throughout).
- Same-type consecutive jobs need **no** setup; switching types costs a
  setup time (`constant_setup(k)` or a full `matrix_setup({...})`).
- The machine **may idle**: waiting for a not-yet-released job can beat
  starting an available job now. The search branches over the next job to
  run, so idle-until-release decisions are explored natively.

## Usage

```python
from scheduler import Job, constant_setup, solve

jobs = [Job("J1", 0, 3, 10, "A"), Job("J2", 1, 2, 6, "B")]
result = solve(jobs, constant_setup(2),
               node_budget=100_000, time_budget=10.0)
print(result.status, result.total_tardiness)
```

`result.status` keeps the three outcomes strictly apart:

| Status      | Meaning                                                        |
|-------------|----------------------------------------------------------------|
| `OPTIMAL`   | search exhausted within budget; tardiness proven minimal       |
| `FEASIBLE`  | budget hit; best complete schedule returned, **not** labelled optimal |
| `UNDECIDED` | budget hit before any complete schedule; `schedule` is `None`  |

`certified_lower_bound` always gives a valid lower bound on the optimum
(the optimum itself when `OPTIMAL`).

## Lower bound

`lower_bound(t, remaining)` takes the max of two relaxations (setups
ignored in both, releases ignored in the second):

1. per-job earliest completion `max(t, r_j) + p_j`, summed minimal tardiness;
2. k-th completion ≥ `t` + k smallest processing times, paired with
   ascending deadlines (rearrangement inequality for the convex cost
   `max(0, c - d)`).

## Files

- `scheduler.py` — model, setups, lower bound, branch and bound
- `example.py` — runnable demo (`python3.11 example.py`)
- `test_scheduler.py` — unittest suite (`python3.11 -m unittest -v`),
  including brute-force permutation checks on random integer and rational
  instances
