# Shared Subplan: Materialize vs Recompute

Pure Python 3.11 (standard library only). No SQL engine is called:
tables are local synthetic objects, operators are plain generators.

## Modules

| file | role |
|------|------|
| `relalg.py` | tables + operators; NULL = three-valued logic, relations are multisets |
| `plan.py` | plan DAG nodes with structural keys (shareable iff structurally equal) |
| `decision.py` | cost model + materialize/recompute/spill decision with rationale |
| `materialize.py` | catalog with atomic publish (no partial tables) and disk spill |
| `engine.py` | counts consumers per subplan, decides, executes with sharing |
| `demo.py` | runnable sample (`python3.11 demo.py`) |
| `test_shared_plan.py` | unittest suite (`python3.11 -m unittest test_shared_plan -v`) |

## Decision rule

For a subplan `S` with `k` consumers:

```
recompute   = k * compute(S)
materialize = compute(S) + write(S) + k * read(S)   # spill rates if over quota
```

* `k == 1` -> always recompute (no reuse possible, no gratuitous materialization)
* else pick the cheaper strategy; if the memory footprint exceeds
  `mem_quota_bytes`, write/read are charged at spill-to-disk rates
* every decision carries a `rationale` string with the driving numbers

## Semantics guarantees

* **NULL**: comparisons with NULL are UNKNOWN; filters keep only TRUE rows;
  NULL join keys never match (`NULL = NULL` is UNKNOWN).
* **Multiset**: duplicates are preserved by scan/filter/project and
  multiplied by joins.
* **Sharing safety**: sharing is keyed on structural identity of the
  *unfiltered* subplan; two consumers with different filters share the
  subplan below their filters, each applying its own filter on top.
* **Atomicity**: a view is published only after being fully built
  (in-memory list, or temp file + atomic `os.replace`); a mid-build
  failure leaves no catalog entry, no partial rows, no leftover files.

## Sample run (abridged)

```
1) Two consumers, different filters, one shared join subplan
  [decision] Join     -> materialize-memory
             2 consumers: compute once + share (21.4 = 16.0 compute + 3.0 write
             + 2.4 reads) beats recomputing 2x (32.0); footprint 384B fits
             memory quota 1048576B -> keep in memory
  shared join materialized once, 5 rows (unfiltered), spilled=False

2) Single-use subplan: no gratuitous materialization
  [decision] Join     -> recompute
             single consumer: no reuse possible; ...
  catalog entries after run: 0

3) Memory quota exceeded: shared subplan spills to disk
  [decision] Filter   -> materialize-spill
             ... footprint 64000B exceeds memory quota 4096B -> spill to disk
  view spilled=True, rows=1000
```

## Tests

`python3.11 -m unittest test_shared_plan -v` — 17 tests, all passing:
three-valued logic, UNKNOWN rejection, NULL join keys, multiset
preservation/multiplication, single-use not materialized, shared subplan
materialized once with per-consumer filters, spill on quota overflow,
atomic publish on failure (memory + spill), and rationale contents.
