# graphcolor

Finite-color vertex coloring library (Python 3.11+, standard library only).

## Features

- **Graph model** (`graphcolor/graph.py`): string vertex/edge IDs, isolated
  vertices preserved, parallel multiedges kept, self-loops detected.
- **Solver** (`graphcolor/solver.py`): precoloring, per-color capacity limits,
  and a three-valued result — `feasible` / `infeasible` (proven) /
  `budget_exhausted` (never confused with infeasible).
- **Reason graph**: every color removal records a `Reason` (edge, capacity,
  precolor, assumption, decision). Backtracking uses a trail that restores
  domains, capacity usage and the reason graph.
- **Reproducible trace**: deterministic vertex/color ordering makes the search
  trace identical across runs.
- **Retractable assumptions**: `push_assumption` / `pop_assumption` with full
  state restoration; used by `solve_lex_min` to produce the lexicographically
  minimal feasible coloring.
- **Color symmetry classes**: optional symmetry breaking enumerates one
  canonical representative per color-permutation class (valid for uniform
  capacities).
- **Independent verifier** (`graphcolor/verifier.py`): validates a coloring
  against all original constraints, sharing no solver logic.
- **Independent enumerator** (`graphcolor/enumerator.py`): brute-force
  `itertools.product` + verifier, used to prove pruning never drops legal
  solutions and to compare solution-class sets with symmetry breaking on/off.
- **Conflict core** (`graphcolor/core.py`): deletion-minimal subset of the
  original constraints (edge IDs, precolored vertices, capacity colors) that
  is still infeasible. If any sub-check exhausts the budget,
  `minimal_proven` is `False` and minimality is not claimed.
- **Intervals** (`graphcolor/intervals.py`): overlap graphs for half-open
  `[start, end)` intervals — touching endpoints do not conflict.

## Usage

```python
from graphcolor import Graph, Solver, solve_lex_min, verify_coloring

g = Graph()
g.add_edge("e_ab", "a", "b")
g.add_edge("e_bc", "b", "c")
g.add_edge("e_ca", "c", "a")

res = Solver(g, 3, capacities={0: 2}, precoloring={"a": 0}).solve()
assert res.status == "feasible"
assert verify_coloring(g, res.coloring, 3, {0: 2}, {"a": 0}) == []

lex = solve_lex_min(g, 3)  # {'a': 0, 'b': 1, 'c': 2}
```

## Run

```sh
python3 demo.py                          # runnable samples
python3 -m unittest discover -s tests -v # test suite (unittest)
```
