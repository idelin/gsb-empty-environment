# Cumulative Scheduling (compulsory-interval propagation)

Pure standard-library Python 3.11+. No SAT/SMT solver: finite start
domains are filtered by timetable (compulsory-interval) propagation and
a small backtracking search; claimed solutions are verified by an
independent checker against the original constraints.

## Model

- `Task(name, starts, duration, demand)`: `starts` is a finite set of
  integer start times (`domain(lo, hi)` builds an inclusive range).
- One resource with fixed `capacity`; while running, a task occupies
  `[start, start + duration)` using `demand` units.
- Zero-duration tasks never occupy the resource.
- A single task whose demand exceeds the capacity fails immediately
  (`ValueError`).

## API

- `propagate(tasks, capacity)` — sound timetable filtering to fixpoint.
  Returns pruned domains, `None` on domain wipe-out, and raises
  `Overload` with `OverloadEvidence` (time window + necessarily
  occupying tasks + required vs capacity) on a proven overload.
- `solve(tasks, capacity)` — returns `{name: start}` or `None`; the
  result is checked by `check_solution` before being returned.
- `check_solution(tasks, capacity, assignment)` — independent checker:
  starts inside original domains, capacity respected at all times.

## Run

```
python3 cumulative.py      # demo instances
python3 -m unittest -v     # tests (incl. randomized brute-force soundness)
```
