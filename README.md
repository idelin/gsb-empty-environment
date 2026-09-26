# Event-Driven Preemptive EDF Scheduler (Uniprocessor)

Python 3.11+, standard library only (`fractions`, `heapq`, `bisect`,
`dataclasses`, `enum`). No solvers, no floats: all constraint data are exact
integers or rationals (`fractions.Fraction`).

## Model

- Each `Task(task_id, release, work, deadline)` has a release time, a work
  amount and a deadline; `work >= 0` and `deadline >= release` are validated.
- Preemptive uniprocessor: at most one job runs at a time, preemption is free.
- A job never runs before its release time (enforced by construction and
  checked by tests).
- Zero-work tasks complete immediately at their release time.
- Ties (same release, same deadline) are broken by task id, stably.
- Event jumping: time advances directly between releases and completions
  (at most `2 * n` events for `n` tasks), never tick by tick, so huge
  integers like `10**12` simulate instantly.

## Outcomes (kept separate)

| Status       | Meaning |
|--------------|---------|
| `OPTIMAL`    | EDF met every deadline. Preemptive EDF is optimal on a uniprocessor (Horn 1974), so feasible and optimal coincide; `result.feasible` is `True`. |
| `INFEASIBLE` | A deadline was missed, hence (by EDF optimality) no feasible schedule exists. `result.evidence` carries a certificate window. |
| `UNDECIDED`  | The event budget `max_events` was exhausted; nothing is claimed. |

## Infeasibility evidence

On a miss, the simulator walks back from the missed deadline `d` over the
recorded timeline to the earliest `t0` such that `[t0, d]` was continuously
busy with jobs whose deadline is `<= d`. Under EDF all of that work was
released at or after `t0`, so the window's demand (work with
`release >= t0` and `deadline <= d`) strictly exceeds its supply `d - t0`.
`verify_evidence(tasks, evidence)` re-checks the certificate independently.

## Usage

```python
from edf_scheduler import Task, Status, simulate

result = simulate([Task(1, 0, 2, 5), Task(2, 1, 1, 3)])
assert result.status is Status.OPTIMAL
for seg in result.segments:        # [start, end) -> task id (None = idle)
    print(seg)
```

Run the demo: `python3 edf_scheduler.py`
Run the tests: `python3 -m unittest -v`

## Sample run (`python3 edf_scheduler.py`)

```
=== feasible set -> OPTIMAL ===
status    : optimal (feasible=True)
events    : 7
schedule  :
  [0, 1)  task 1
  [1, 2)  task 2
  [2, 3)  task 1
  [3, 8)  idle
  [8, 9)  task 4
completed : task 1 @ 3, task 2 @ 2, task 3 @ 2, task 4 @ 9

=== infeasible set -> INFEASIBLE with evidence ===
status    : infeasible (feasible=False)
events    : 4
schedule  :
  [0, 3)  task 1
  [3, 6)  task 2
completed : task 1 @ 3, task 2 @ 6
evidence  : task 2 missed its deadline at 6; window [0, 4] has demand 6 > supply 4
verified  : True

=== tiny event budget -> UNDECIDED ===
status    : undecided (feasible=False)
events    : 3
```

## Test results (`python3 -m unittest -v`)

18 tests, all passing (ran in ~0.001s):

- `TestFeasible` — preemption by earlier deadline; stable id tie-break on
  simultaneous release with equal deadlines; zero-work immediate completion;
  no execution before release; idle-gap recording; exact rational arithmetic;
  event jumping with `10**12`-scale work in 4 events; empty instance.
- `TestInfeasible` — overloaded window certificate (`demand 6 > supply 4`);
  evidence window correctly excludes later-deadline filler work
  (`[1, 4]`, `demand 6 > supply 3`); zero-length window when
  `deadline == release` with positive work; rational overload.
- `TestBudget` — tiny `max_events` yields `UNDECIDED`; the same instance is
  decided (`OPTIMAL`) with a sufficient budget.
- `TestValidation` — negative work, `deadline < release` and duplicate ids
  rejected; plain tuple input accepted.

Every feasible-schedule test also re-validates the produced timeline:
contiguity, no pre-release execution, per-task executed work equals its
requirement, and all completions meet their deadlines.

```
Ran 18 tests in 0.001s

OK
```
