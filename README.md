# fdopt — finite-domain integer minimization

Pure Python 3.11 (standard library only, no SAT/SMT solvers).
Branch-and-bound over finite integer domains with integer linear
constraints and an integer linear objective.

## Guarantees

- **Feasible upper bound**: the incumbent is always a verified feasible
  solution; its objective is the current upper bound.
- **Reliable lower bound**: a node's bound is the cheapest
  constraint-ignoring completion of its partial assignment, so it never
  exceeds the true subtree optimum. The reported global lower bound is
  the minimum over the unexplored frontier and never exceeds the true
  optimum.
- **Budget interruption** (`max_nodes` and/or `time_limit`): returns
  the best known solution plus the current gap with status `FEASIBLE`
  (incumbent available) or `UNKNOWN` (nothing found, nothing proven).
  Optimality is claimed only when the search space is exhausted.
- **No feasible solution vs. proven infeasible**: `UNKNOWN`
  (interrupted, no incumbent) is strictly distinguished from
  `INFEASIBLE` (search exhausted, infeasibility proven).
- **Independent verification**: any claimed solution is re-checked
  against the original constraints by `checker.py` (which shares no
  code with the search) before being returned.
- **Deterministic tie-break**: among equal-objective optima the
  lexicographically smallest assignment in variable order is returned.

## Files

- `model.py` — `Problem`, `LinearConstraint`, `Sense`
- `solver.py` — `solve(problem, max_nodes=None, time_limit=None)` -> `SolveResult`
- `checker.py` — independent assignment/objective checker
- `example.py` — runnable demo (`python3.11 example.py`)
- `tests/test_solver.py` — unittest suite, including randomized
  cross-validation against brute-force enumeration

## Usage

    from model import LinearConstraint, Problem, Sense
    from solver import solve

    problem = Problem(
        domains=[[0, 1, 2], [0, 1, 2]],
        objective=(1, 2),
        constraints=[LinearConstraint((1, 1), Sense.GE, 2)],
    )
    result = solve(problem, max_nodes=10_000, time_limit=5.0)
    print(result.status, result.solution,
          result.lower_bound, result.upper_bound, result.gap)

## Tests

    python3.11 -m unittest discover -s tests -v

## Notes

- The lower bound is a simple relaxation; it is valid but can be weak
  on loosely constrained instances, which shows up as a large gap on
  interruption. It never overestimates the optimum.
- All arithmetic is exact integer arithmetic.
