# DPLL SAT Solver (Python 3.11, stdlib only)

A small DPLL solver featuring:

- **Unit propagation** via the **two-watched-literal** scheme.
- **Assumption solving with retraction**: `solve(assumptions=[...])` only
  asserts hypotheses for that query; the trail is backtracked to level 0
  afterwards, so the same solver accepts repeated/incremental queries.
- **Independent verification**: before any SAT answer is returned, the
  standalone `verify_model` evaluates the original clauses.
- Empty clause => immediate UNSAT; tautological clauses are ignored; empty
  formula => SAT.
- On backtrack, watched literals are not restored (moving a watch only
  happens when a literal becomes false, so moves stay valid after
  unassignment); the assignment trail and propagation frontier are restored,
  which guarantees no propagation is missed.

## Files

- `dpll.py` — solver (`DPLLSolver`), independent checker (`verify_model`),
  exhaustive reference (`brute_force_sat`).
- `test_dpll.py` — 15 unittest cases, including 150 random CNFs cross-checked
  against brute force and a pigeonhole UNSAT instance.
- `demo.py` — runnable samples.

## Run

```sh
python3.11 demo.py
python3.11 -m unittest test_dpll -v
```

## API sketch

```python
from dpll import DPLLSolver, verify_model

s = DPLLSolver(num_vars=3)
s.add_clause([1, 2])        # x1 v x2
s.add_clause([-1, 3])       # ~x1 v x3

model = s.solve()                       # -> {1: ..., 2: ..., 3: ...} or None
assert model is None or verify_model(s.original_clauses, model)

s.solve(assumptions=[-3])   # query under a hypothesis; auto-retracted
s.solve()                   # solver state is unchanged
```
