# cspsolver — lexicographic finite-domain constraint solver

Pure Python 3.11, standard library only. No SAT/SMT backends: search is an
explicit depth-first branch & bound you can audit.

## Guarantees

- **Hard constraints are absolute.** If they conflict, the model is UNSAT.
  No penalty, however large, can buy through a hard conflict.
- **Soft constraints** carry non-negative **integer** weights and a priority
  **level**. Violations add integer penalties per level.
- **Lexicographic objectives.** The objective is a tuple of per-level
  penalties compared level by level (level 0 first). One unit of high-level
  penalty can never be offset by any amount of low-level gain.
- **Negative weights are rejected** at model construction (`ValueError`);
  non-integer weights raise `TypeError`.
- **Independent verification.** Before claiming a solution, the solver
  re-checks it with `cspsolver.checker.check_assignment`, a separate code
  path that validates domain membership, every hard constraint, and the
  exact penalty vector against the original model.
- **Explainability.** `SolveResult.explain()` lists, per level, the penalty,
  the certified lower bound (equal to the penalty once optimality is proven),
  and every violated soft constraint with the assignment that violated it.

## Layout

- `cspsolver/model.py` — variables, hard/soft constraints, validation
- `cspsolver/solver.py` — branch & bound, lexicographic pruning, explanations
- `cspsolver/checker.py` — independent solution checker
- `examples/run_sample.py` — runnable demo
- `tests/test_solver.py` — unittest suite, incl. brute-force cross-check

## Run

```sh
python3.11 examples/run_sample.py
python3.11 -m unittest discover -s tests -v
```

## API sketch

```python
from cspsolver import Model, Solver, Variable

m = Model()
m.add_variable(Variable("x", domain=(0, 1, 2)))
m.add_hard("x_nonzero", ("x",), lambda x: x != 0)
m.add_soft("prefer_2", ("x",), lambda x: x == 2, weight=3, level=0)

result = Solver(m).solve()
print(result.status, result.objective)
print(result.explain())
```

## Notes on bounds

The per-node lower bound sums, per level, the weight of every soft
constraint that is already violated or can no longer be satisfied by any
extension of the partial assignment (extension checks are capped at
`EXTENSION_LIMIT` combinations and stay conservative beyond it). A node is
pruned when its bound is lexicographically `>=` the incumbent, which is what
makes the certified per-level bounds exact at termination.
