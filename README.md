# Window Sort Sharing (pure Python, no SQL engine)

Groups multiple window definitions by partition/order compatibility so one
physical sort is reused; ROWS frame boundaries stay per-window, so shared
sorts never share results. Requires Python 3.11+, standard library only.

## Files

- `window_sort_sharing.py` — planner, executor, synthetic relation, demo.
- `test_window_sort_sharing.py` — unittest suite (15 tests).

## Sharing rules

- Same `PARTITION BY` keys + identical normalized `ORDER BY` keys share a sort.
- Different ROWS frames may share the sort but compute their own results.
- ASC vs DESC on the same column cannot be reused directly.
- NULLS FIRST/LAST differences (explicit or defaulted) prevent sharing;
  defaults: ASC -> NULLS LAST, DESC -> NULLS FIRST. NULL placement is
  independent of direction (DESC does not flip it).

## Relation semantics

- Relation is a local list of dict rows: a bag (multiset); duplicates are
  kept and counted individually.
- NULL is `None`; NULL partition keys group together.
- `SUM`/`AVG`/`COUNT(col)` ignore NULLs; empty or all-NULL frames give
  `None` for `SUM`/`AVG` and `0` for `COUNT`.

## Run

```sh
python3.11 window_sort_sharing.py            # demo: plan + sort count + results
python3.11 -m unittest test_window_sort_sharing -v
```
