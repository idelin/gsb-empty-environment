# minidb — atomic primary-key changes with stable entity identity

A miniature relational store (Python 3.11+, standard library only) that
treats a primary-key change as a first-class, transactional operation:
the old index entry is retracted and the new one established atomically,
foreign keys across tables are revised in the same transaction, and two
entities may swap keys — or N entities may rotate keys in a cycle —
without tripping the uniqueness check midway.

## Layout

- `minidb/core.py` — `Database`, `Table`, `Transaction`; stable entity
  ids, phased commit (detach old keys → attach new keys → revise foreign
  keys), checkpoint/rollback, frozen snapshots.
- `minidb/fixtures.py` — local JSON fixture loading; event dedup,
  version-based isolation of late events, per-transaction grouping.
- `minidb/verifier.py` — standalone full-model verification: index ↔ row
  bijection, foreign-key resolution, reverse-reference equality, retired
  id reuse, commit-seq ordering. Runs on the live db or any snapshot.
- `fixtures/changes.json` — sample change events (txn id, table, primary
  key, before/after values, commit sequence). No message platform.
- `demo.py` — runnable end-to-end sample.
- `tests/test_minidb.py` — `unittest` suite.

## Semantics

- **Atomic index swap** — commit detaches every old key before attaching
  any new key, so swaps and cycles commit as one unit; if any new key is
  still occupied, the *whole* transaction fails and the checkpoint is
  restored (index, rows, reverse references).
- **Stable identity** — rows are addressed internally by an entity id
  that never changes and is never reused (deleted ids are retired).
  Foreign keys follow the *entity*, so references survive key changes
  and swaps point at the same entity afterwards.
- **Old keys miss** — once a key moves, point lookups on the old key
  return nothing.
- **Duplicate & late events** — re-delivered events (same txn, table,
  old key, commit seq) are skipped; events whose `before` key no longer
  resolves, or whose commit seq is not newer than the entity's last
  applied seq, are isolated as stale instead of moving anything.
- **Snapshots** — `db.snapshot()` returns a frozen, independently
  verifiable copy; mutations on it raise `FrozenSnapshotError`.

## Run

```sh
python3 demo.py                        # uses fixtures/changes.json
python3 -m unittest discover -s tests -v
```

## Sample output (abridged)

```
== applying fixture fixtures/changes.json ==
  committed  txn-001            # 1 -> 10
  committed  txn-002            # swap 2 <-> 3
  committed  txn-003            # cycle 4 -> 5 -> 6 -> 4
  FAILED     txn-004: users.id=4 is occupied; transaction txn-004 fails as a whole
  duplicate  txn-001 seq=1 users.id 1->10 (skipped)
  stale      txn-006 seq=1 users.id 10->15 (isolated)
  stale      txn-005 seq=9 users.id 1->99 (isolated)
  verify(final): OK
  verify(pre-change snapshot): OK
```

## Test results

```
Ran 17 tests in 0.004s
OK
```
