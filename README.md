# mince — minimal counterexample finder for query-plan equivalence

Generates inputs over a small finite data domain and searches for a
**minimal counterexample**: a synthetic database on which two query plans
return different result **multisets**. Pure Python 3.11+ standard library;
no SQL engine is invoked — plans are trees of relational-algebra
operators evaluated directly.

## Semantics (explicit)

- **NULL**: singleton sentinel (`relalg.NULL`). Any comparison involving
  NULL yields UNKNOWN (three-valued logic); `Select` keeps only TRUE rows;
  join conditions match only on TRUE (NULL never equals NULL).
- **Outer join**: `LeftJoin` pads unmatched left rows with NULLs.
- **Bag semantics**: every operator preserves duplicates. Results are
  compared with `collections.Counter`:
  - row **order** is irrelevant (no ORDER BY) — not a difference;
  - differing **multiplicities** of a row **are** a difference.
- **No proof claims**: if the budget (or the whole bounded space) is
  exhausted without a mismatch, the result is reported as
  `budget_exhausted` / `space_exhausted` — a bounded check, **not** a
  proof of equivalence.

## Supported plans

`Scan`, `Select` (filter), `Project` (bag projection), `Join` (inner
theta-join), `LeftJoin` (left outer join). Predicates: `Cmp` (=, !=, <,
<=, >, >=), `And`, `Or`, `Not`, `IsNull`, `Col`, `Lit`. Column names must
be disjoint across join inputs.

## Files

- `relalg.py` — NULL, three-valued logic, expressions, operators, evaluator
- `generate.py` — enumeration of tables/databases over a finite domain
- `search.py` — multiset comparison, counterexample search, greedy
  row-deletion minimizer (yields a 1-minimal counterexample: removing any
  single remaining row makes the plans agree)
- `cli.py` — runnable demo (three plan pairs)
- `test_relalg.py`, `test_search.py` — unittest suites

## Run

```
python3 cli.py                       # demo: search + minimize + report
python3 -m unittest discover -v      # run all tests
```

## Sample output (`python3 cli.py`)

```
====================================================================
Pair 1:  sigma_{b>0}(R LOJ S)   vs.   R LOJ sigma_{b>0}(S)
====================================================================
databases checked: 56
STATUS: COUNTEREXAMPLE FOUND (plans are NOT equivalent)
minimal counterexample input (removing any row makes plans agree):
  R = { (0) }
  S = { }
  plan 1 -> { }
  plan 2 -> { (0, NULL, NULL) }

====================================================================
Pair 2:  sigma_{a>0}(R JOIN S)   vs.   sigma_{a>0}(R) JOIN S
====================================================================
databases checked: 550
STATUS: NO counterexample found (space_exhausted)
  entire bounded space (max_rows=2, domain size=3) checked
  NOTE: this is only a bounded check, NOT a proof of equivalence.

====================================================================
Pair 3:  pi_a(R JOIN S)   vs.   pi_a(R)  [join elimination]
====================================================================
databases checked: 56
STATUS: COUNTEREXAMPLE FOUND (plans are NOT equivalent)
minimal counterexample input (removing any row makes plans agree):
  R = { (0) }
  S = { }
  plan 1 -> { }
  plan 2 -> { (0) }
```

## Test results (`python3 -m unittest discover`)

```
Ran 28 tests in 0.01s
OK
```

Covers: NULL singleton semantics, three-valued logic truth tables,
comparisons with NULL, filter/join/outer-join behavior, bag preservation
under projection and joins, order-insensitive / multiplicity-sensitive
comparison, generator sizing, counterexample discovery, 1-minimality of
the shrinker, and budget/time cutoffs reported as non-proofs.
