# Allen interval algebra

`allen.py` implements, using only the Python 3.11 standard library:

- the thirteen atomic Allen interval relations and disjunctive relation masks;
- a composition table synthesized from all endpoint weak orders over six endpoints;
- AC-3-style path consistency with an incremental revision queue;
- revision evidence, conflict explanations, `push()`/`pop()` snapshots, and restorable search state;
- complete atomic-relation backtracking with endpoint equality/order construction;
- independent small-integer interval enumeration for cross-checking;
- distinct exact types for `WallClock`, `TimePoint`, and `Duration`.

Path consistency returning non-empty domains means only “not locally refuted”.
`find_integer_witness()` continues global search and reports `satisfiable`,
`unsatisfiable`, or `unknown` when a branch/propagation budget is exhausted.

Run the tests with:

```sh
python3.11 -m unittest -v test_allen.py
```
