# Grouped Retractable TopK

Python 3.11, standard library only, tests with `unittest`. No message
platform: changes come from local JSON-line fixtures, each event carrying
`tx_id`, `table`, `pk`, `before`, `after`, `commit_seq`.

## Layout

- `topk/keys.py` — `SortSpec`/`FieldSpec`: multi-field sort, per-field
  `desc` and `nulls_last`, ties always broken by id (stable).
- `topk/store.py` — per-group segmented store. The best `window_size`
  entries stay in a bounded in-memory window; remaining candidates live in
  sorted page files on disk. `BlockStats` counts page-block reads/writes.
- `topk/index.py` — `TopKIndex`: applies change batches, emits stable-id
  diffs `{group: {entered, exited}}` (rank order), serves fixed-version
  `Snapshot` with `page(group, offset, limit)`, `save()`/`load()` recovery.
- `topk/reference.py` — independent full-sort reference for verification.
- `fixtures/changes.jsonl` — sample change fixture; `main.py` — run sample.
- `tests/test_topk.py` — unittest suite.

## Design notes

- **Retractable TopK**: the window keeps `window_size >= K` candidates, so
  deleting rank 1 promotes the old rank K+1 from the window or from the
  head page. Backfill reads only the leading page(s) — never a full group
  scan (verified: 117 pages on disk, delete touches 1 block).
- **Update = remove + add** keyed by id, so changing a sort key never
  duplicates an entry; a group change migrates the id out of the old group.
- **K=0** short-circuits to empty results and empty diffs.
- **Snapshots** materialize per-group TopK at the current `commit_seq`;
  pagination over an old snapshot is pinned to that version while new
  commits keep arriving.
- Pages split at `2 * page_size`; per-group in-memory state is the window
  plus an id→location index, so tiny `window_size`/`page_size` forces most
  data to external segments.

## Run

```
python3.11 main.py                          # run sample
python3.11 -m unittest discover -s tests -v # tests
```

## Sample output (abridged)

```
commit 4: {"A": {"entered": ["u4"], "exited": ["u2"]}}
commit 5: {"A": {"entered": [], "exited": ["u3"]}, "B": {"entered": ["u3"], "exited": []}}
group A: ['u8(score=75,name=hal)', 'u1(score=65,name=amy)', 'u4(score=60,name=dan)']
snap1 version=4 page(A,0,2)=['u3', 'u1']   # pinned to old version
snap2 version=6 page(A,0,2)=['u8', 'u1']
restored topk identical: True
group 'bulk' spilled to 99 pages; delete rank-1 -> new top3=['b0498', 'b0497', 'b0496']
blocks touched by that delete: {'blocks_read': 1, 'blocks_written': 1}
```

## Test results

13 tests, all passing (Python 3.11):

- semantics: delete-first promotes K+1, K=0 empty, update without
  duplicates, group migration, null placement, multi-field order,
  id tie-break at the last rank, other-table events ignored
- block bound: delete backfill over 117 spilled pages reads 1 block
- recovery: save/load round-trip, then continued writes match reference
- snapshot: fixed-version pagination, page stitching equals full read
- randomized: 800 seeded ops, hot-group skew (70% one group), tiny memory
  (window=4, page=3), nulls and group moves — index matches the
  independent full-sort reference after every batch
- fixture: replaying `fixtures/changes.jsonl` matches the reference
