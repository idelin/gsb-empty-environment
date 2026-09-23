# Ordered Non-Commutative Matrix Fold

Maintains, per group, the ordered product of 2x2 integer matrices
(mod `m`) under a stable sort key. Matrix multiplication is
non-commutative, so the fold respects sequence order; deletions never
rely on matrix inverses (elements may be singular). Incremental results
are verified against a full ordered scan after every applied event.

## Design

- `matrix.py` — 2x2 matrix arithmetic over Z/mZ; `fold` of an empty
  sequence is the identity matrix.
- `treap.py` — randomized treap (balanced interval tree, deterministic
  hash priorities). Each node caches `agg = agg(left) * value * agg(right)`,
  so the root aggregate is the in-order fold. Insert/delete are
  split/merge based, expected O(log n).
- `store.py` — `GroupStore` applies `ChangeEvent`s (tx id, table, pk,
  before/after images, commit seq). Cross-group moves are atomic: the
  event is fully validated (pk uniqueness, row existence) before any
  tree is touched. Empty groups fold to the identity.
- `replay.py` — loads a local JSON fixture (no message platform),
  sorts events by `commit_seq`, applies them, and checks the
  incremental aggregate of every affected group against a full scan.
- `main.py` — demo CLI: `python3 main.py [fixture.json]`.

## Fixture format

```json
{
  "modulus": 1000000007,
  "events": [
    {"tx_id": "tx-1", "table": "items", "pk": "a", "commit_seq": 1,
     "before": null,
     "after": {"group": "g1", "sort_key": 10, "matrix": [[1, 1], [1, 0]]}}
  ]
}
```

`before = null` is an insert, `after = null` a delete, both present an
update — or an atomic migration when the group changes.

## Run

```sh
python3 main.py            # replay fixtures/sample_changes.json
python3 -m unittest -v     # 15 tests
```

Requires Python 3.11+, standard library only.
