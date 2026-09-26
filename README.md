# Finite-Domain Projected Solution Enumerator

A small Python 3.11, standard-library-only backtracking enumerator. It uses no SAT/SMT library or solver.

## Features

- Enumerates all solutions or the first `N` with `max_solutions`.
- Supports finite variable domains given as ordered values.
- Adds one full-solution blocking clause only when a projected solution is emitted.
- De-duplicates projected answers; assignments that differ only in auxiliary variables yield one output.
- Treats explicit `projection=()` as the empty projection, which has at most one solution.
- Default projection (`projection=None`) contains every variable and therefore enumerates complete solutions.
- Keeps an explicit search stack across `node_budget` pauses; call `run()` again to resume.
- Runs an independent checker against the original constraints before claiming a candidate.

## Usage

```python
from solution_enumerator import ProjectingEnumerator

variables = {"x": (0, 1, 2), "a": (0, 1, 2, 3)}
constraints = (lambda env: env["x"] + env["a"] <= 3,)

enumerator = ProjectingEnumerator(variables, constraints, projection=("x",))
result = enumerator.run(node_budget=2)
# Resume on the same object:
more = enumerator.run()
```

Each result item has:

- `values`: projected variable values.
- `witness`: the complete original assignment, including auxiliary variables.

`enumerator.search_stack` exposes frames as `(variable, next_domain_index)`. `status` is `complete`, `solution_limit`, or `node_budget`.

## Run the sample

```bash
python3.11 example_run.py
```

Observed output:

```text
Project onto x; stop after a two-node budget:
  {'x': 0} witness {'x': 0, 'a': 0}
 status: node_budget resumable: True
 stack: (('x', 1), ('a', 1))
Resume:
  {'x': 1} witness {'x': 1, 'a': 0}
  {'x': 2} witness {'x': 2, 'a': 0}
 status: complete complete: True
Empty projection:
 solutions: [{}]
 complete: True
```

## Tests

```bash
python3.11 -m unittest -v
```

Result on Python 3.11.15:

```text
Ran 14 tests in 0.001s

OK
```
