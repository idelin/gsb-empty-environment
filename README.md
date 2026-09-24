# composite-index

A composite-index access-path **compiler** plus an MVCC **executor**,
implemented in pure Python 3.11 standard library. No SQL engine, no
"return a plan name" shortcuts: predicates compile to concrete,
non-overlapping scan intervals with residual guards and a cost breakdown,
and a real iterator executes the chosen plan against versioned storage.

## Layout

- `composite_index/core.py` — key ordering: per-column ASC/DESC + NULLS
  FIRST/LAST comparison, `MIN`/`MAX` endpoint sentinels.
- `composite_index/predicates.py` — typed predicate tree
  (`Cmp/In/NotIn/IsNull/NotNull/And/Or/Not`), SQL three-valued evaluation
  (`TRUE/FALSE/UNKNOWN`, only `TRUE` rows qualify), negation normal form.
- `composite_index/mvcc.py` — versioned table and indexes. Every write
  bumps a commit version and appends to version chains; readers pin a
  snapshot. Key updates tombstone the old key and add the new one, so an
  old cursor keeps reading the row under its old key.
- `composite_index/compiler.py` — the compiler:
  - per-column range algebra (intersect/union/normalize) in *index order*
    (direction- and NULL-placement-aware);
  - composite prefix rules: point-constrained prefix columns extend the
    key prefix (cartesian product for `IN` lists), the first range
    column terminates the prefix with open/closed full-length endpoints,
    and unconstrained columns yield a whole-prefix interval — a
    suffix-only constraint therefore becomes a full-index interval plus
    residual, never a narrow prefix;
  - OR branches become interval unions, normalized to disjoint intervals
    (overlapping intervals merge only into their exact contiguous hull,
    guarded by the OR of the branch predicates);
  - interval budget: exceeding it collapses the set into one superset
    hull guarded by the full predicate — branches are never dropped;
  - cost model (`page_read`, `lookup`, `compare` unit costs) and
    deterministic plan selection over per-index plans, index-merge
    (union) plans and full scans.
- `composite_index/executor.py` — the iterator: streams index entries at
  the snapshot version, de-duplicates row ids across arms/intervals (a
  row hit by two indexes is read and emitted once; two physical rows with
  equal values remain two results), applies residual guards, counts
  pages/entries/lookups/compares actually performed.
- `composite_index/database.py` — facade: snapshots, paged `execute` /
  `fetch`, and resume tokens. Tokens are HMAC-signed and bind the plan
  fingerprint (predicate + parameters), the snapshot id and the exact
  cursor position; tampering, plan/parameter mismatch, stale replay and
  reclaimed snapshots are all rejected with `TokenError`.
- `composite_index/interpreter.py` — independent full-scan interpreter
  used as the correctness oracle.

## Usage

```python
from composite_index import Database, ColumnSpec, Cmp, In, Or, NULL

db = Database(["a", "b"])
db.create_index("idx_ab", [ColumnSpec("a"), ColumnSpec("b", desc=True)])
db.insert({"a": 1, "b": NULL})

page = db.execute(Or((Cmp("a", "eq", 1), In("b", (0, 1)))), page_size=100)
print(page.plan)        # intervals, residuals, estimate, all candidates
while page.next_token:
    page = db.fetch(page.next_token)
print(page.executed)    # pages/entries/lookups/compares actually done
```

## Tests

```
python3 -m unittest discover -s tests -v
python3 examples/demo.py
```

The acceptance suite exhaustively sweeps generated predicates over a
small domain (including NULLs and duplicate-valued rows) and compares
every candidate plan — forced, not just the chosen one — against the
independent interpreter, inserting updates, deletes and key migrations
between pages. It covers (a ASC, b DESC) prefixes, NULL endpoints,
overlapping `IN` sets, `NOT` of UNKNOWN, multi-index row-id dedup,
interval-budget degradation, token misuse, and cost/executed-count
reproducibility.
