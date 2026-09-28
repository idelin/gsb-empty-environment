# Cumulative scheduling with compulsory-part propagation

Pure Python 3.11, standard library only. No SAT/SMT solver is used.

- `cumulative.py` — model, timetable propagation, solver, independent checker.
  - `propagate(tasks, capacity)` prunes start domains using compulsory
    parts `[lst, est + duration)`; it only removes values that cannot
    appear in any feasible schedule, and raises `OverloadError` with
    time-window evidence (`OverloadEvidence`) when compulsory demand
    exceeds capacity, or `DomainWipeoutError` with per-value blocking
    times when a domain becomes empty.
  - A single task whose demand exceeds capacity fails immediately
    (zero-duration tasks occupy nothing and are exempt).
  - `solve(tasks, capacity)` does depth-first search with propagation;
    every returned schedule is verified by the independent `check`
    against the original domains and capacity.
- `test_cumulative.py` — unit tests, including randomized soundness
  checks against brute-force enumeration.
- `example.py` — runnable demo.

Run:

    python3 example.py
    python3 -m unittest -v
