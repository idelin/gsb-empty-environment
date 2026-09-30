# Tuple-Table CSP Solver (pure Python 3.11, stdlib only)

Finite-domain constraint solver over tuple tables. No SAT/SMT libraries;
search is plain backtracking with GAC (generalized arc consistency)
maintained incrementally on compressed tables.

## Features

- **Shared-prefix compression**: tuples are stored in a trie; common
  prefixes are stored once (`TupleTable.node_count()` vs. flat cells).
- **Duplicate tuples counted once**: re-inserting an existing tuple is a
  no-op, so support counters are never inflated.
- **Support maintenance on domain change**: removing a value from a
  variable domain synchronously invalidates every tuple using it and
  decrements `(position, value)` support counters; a trail allows exact
  undo on backtrack.
- **Positive and negative tables**: positive = allowed tuples,
  negative = forbidden tuples.
- **Edge-case semantics**: an empty positive table is infeasible; an
  empty negative table imposes no restriction.
- **Independent checker**: any claimed solution is verified against the
  original tuple data (`checker.verify_solution`), not the trie.

## Files

- `tuple_table.py` - trie-compressed table + support bookkeeping
- `csp.py` - CSP model, backtracking solver with GAC propagation
- `checker.py` - independent verifier + brute-force enumerator
- `demo.py` - runnable examples
- `test_tuple_table.py` - unittest suite (incl. randomized cross-check
  of solver vs. brute force on 200 random CSPs)

## Run

```sh
python3.11 demo.py                  # run the examples
python3.11 -m unittest -v           # run the tests
```

## Example

```python
from csp import CSP, solve
from tuple_table import TupleTable

csp = CSP()
for v in "abc":
    csp.add_variable(v, {"r", "g", "b"})
for u, v in [("a", "b"), ("b", "c"), ("a", "c")]:
    csp.add_constraint([u, v], [(c, c) for c in "rgb"],
                       polarity=TupleTable.NEGATIVE)   # proper 3-coloring
print(solve(csp))   # e.g. {'a': 'b', 'b': 'g', 'c': 'r'} (checker-verified)
```

## Test results

`python3.11 -m unittest` -> 17 tests, OK (includes a randomized
cross-check: solver SAT/UNSAT verdicts match exhaustive enumeration, and
every returned solution passes the independent checker).
