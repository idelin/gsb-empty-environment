# DPLL SAT Solver (pure Python 3.11, stdlib only)

A self-contained SAT solver implementing **DPLL** with **unit propagation**
and the **two-watched-literal** scheme. No SAT/SMT libraries are used.

## Files

- `sat_solver.py` — solver, independent model checker, finite-domain encoder
- `test_sat_solver.py` — unittest suite (24 tests, incl. brute-force cross-validation)
- `example.py` — runnable demo (graph coloring, assumptions, pigeonhole)

## Features

- **DPLL search** with chronological backtracking over the finite (Boolean) domain.
- **Unit propagation via two-watched literals**: each clause watches two
  literals; only assignments that falsify a watched literal trigger a scan.
- **Assumption solving & retraction**: `solve(assumptions=[...])` asserts
  literals for one call only; the solver always returns to decision level 0,
  so it can be reused incrementally (`add_clause` between solves works too).
- **Verifiable results**: every SAT claim is checked by an *independent*
  checker (`check_model`) against the original clauses exactly as added;
  a mismatch raises instead of returning a wrong model. UNSAT results carry
  an explanation with search statistics (decisions/conflicts/propagations/
  depth) stating that the search space was exhausted.
- **Edge cases**: empty clause → immediate UNSAT (no search); tautological
  clauses ignored; empty formula → SAT.
- **Backtracking & watches**: un-assignment preserves the watch invariant
  (a watched literal is never false unless the clause is unit/conflicting),
  so watch lists need no restoration on backtrack and no propagation is lost.
  Level-0 (unit-clause) assignments persist across solves, as they follow
  from the formula alone.
- **Finite-domain variables**: `FiniteDomainEncoder` one-hot encodes
  variables with finite domains into exactly-one CNF constraints.

## Usage

```python
from sat_solver import Solver, Status, check_model

s = Solver()
s.add_clause([1, 2])    # x1 or x2
s.add_clause([-1, 3])   # x1 -> x3

res = s.solve(assumptions=[-2])   # temporary assumption, auto-retracted
assert res.status is Status.SAT and res.verified
print(res.model)                  # {1: True, 2: False, 3: True}

res2 = s.solve(assumptions=[-1, -2])  # UNSAT under these assumptions
print(res2.explanation)               # exhaustive-search / contradiction info
```

## Run

```
python3 example.py            # demo
python3 -m unittest test_sat_solver -v   # tests
```

## Sample output (python3 example.py)

```
==================================================================
1. Graph 3-coloring (finite-domain vars) + assumptions/retraction
==================================================================
status: SAT (verified=True)
coloring: {'v0': 'red', 'v1': 'green', 'v2': 'blue', 'v3': 'red', 'v4': 'green'}
explanation: SAT: assignment found and independently verified against all 38 original clauses (3 decisions, 15 propagations).

assuming v0=v1=red -> UNSAT
explanation: UNSAT under assumptions [1, 4]: unit propagation derives a contradiction at the assumption level (1 propagations); the search tree is empty, so no satisfying assignment exists under these assumptions.

after retraction, assuming only v0=red -> SAT
coloring: {'v0': 'red', 'v1': 'green', 'v2': 'blue', 'v3': 'red', 'v4': 'green'}

==================================================================
2. Pigeonhole: 4 pigeons, 3 holes (UNSAT, exhaustive search)
==================================================================
status: UNSAT
explanation: UNSAT: exhaustive DPLL search completed - 16 decisions, 9 conflicts, 59 unit propagations, 16 backtracks, maximum depth 4. Every branch of the search tree ends in a conflict, so no satisfying assignment exists.

==================================================================
3. Edge cases
==================================================================
empty formula          -> SAT (verified=True)
tautology-only formula -> SAT, working clauses kept: 0
empty clause           -> UNSAT: UNSAT at decision level 0: an empty clause or contradictory unit clauses were added; no search is needed.
unit propagation chain -> SAT, model {1: True, 2: True, 3: True}, independent check: True
```

## Test results (python3 -m unittest test_sat_solver)

```
Ran 24 tests in 0.014s

OK
```

The suite covers: empty formula/clause, tautologies, duplicate literals,
unit-propagation chains, forced backtracking, pigeonhole SAT/UNSAT,
assumption solving + retraction + state cleanliness, incremental clause
addition, the independent checker (accept/reject), finite-domain encoding
(incl. 2-coloring of a triangle = UNSAT), and 350 randomized formulas
cross-validated against exhaustive enumeration (with and without
assumptions).
