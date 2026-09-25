# booldag — incremental boolean-expression DAG over rational interval sets

Pure standard library, Python 3.11. Exact rational arithmetic only
(`fractions.Fraction`); floats are rejected, so topology (connectedness,
emptiness, isolated points) is decided exactly — no epsilon patches.

## Layout

- `booldag/intervals.py` — normalized finite unions of open/closed rational
  intervals (`IntervalSet`): union / intersect / complement / subtract,
  connected components, isolated points. Touching intervals merge only when
  the shared endpoint is covered, so `[0,1] ∪ (1,2]` is the single component
  `[0,2]` while `[0,1) ∪ (1,2]` stays disconnected.
- `booldag/dag.py` — the expression DAG:
  - `Leaf`: mutable source; concrete set, defined-empty, or UNKNOWN
    (`value is None`). Mutations: `set / add / subtract / clear / set_unknown`.
  - `OpNode`: `union` / `intersect` / `diff` over parents.
  - Incremental: a leaf edit dirties only its descendant cone; `eval_count`
    proves untouched nodes are never re-evaluated, and a transaction with N
    leaf edits triggers exactly one re-evaluation per dependent.
  - Provenance: each node carries `pieces = (interval, frozenset(leaves))`,
    greedily minimized. `graph.prove(node, x)` back-references the minimal
    leaf set witnessing point `x`. Witness sets are sets, so a leaf arriving
    via two DAG paths counts once and one `clear()` retracts it everywhere.
  - `graph.transaction()` batches edits; exception or `tx.rollback()`
    restores leaf states and re-evaluates only the affected cone.
  - `graph.snapshot()` freezes all values/proofs; snapshots are immutable
    and isolated from later edits.
  - `add_parent` rejects edges that would close a cycle (`CycleError`).
- `booldag/oracle.py` — independent checker: probes every endpoint and every
  rational gap midpoint of the leaf states and evaluates the boolean
  expression directly, no shared code with the DAG's interval algebra.
- `tests/test_booldag.py` — 21 unittest cases: interval semantics, increment-
  ality, two-path retraction, proof minimality/soundness, transactions,
  rollback, snapshot isolation, cycle rejection, leaf removal/correction, and
  randomized DAG-vs-oracle cross-checks (seeded).
- `demo.py` — runnable walkthrough of every feature above.

## Run

```sh
python3.11 -m unittest discover -s tests -v
python3.11 demo.py
```

## Semantics notes

- UNKNOWN propagates conservatively: any unknown operand makes an op node
  unknown; it is distinct from the defined empty set and from an isolated
  point `{x}` (`[x,x]`).
- Difference is exact about endpoint closedness: `[0,2] \ (1,2] = [0,1]`,
  `[0,1] \ [1,2] = [0,1)`.
- Proof soundness is tested by replaying the expression with only the proven
  leaves (others emptied): the point must still be covered.
