"""Runnable demo: deterministic group-quota sampling end to end.

Run with:  python3.11 demo.py
"""

import json

from group_quota_sampler import Column, GroupQuotaSampler, TableBatch

COLS = [
    Column("emp_id", "int"),
    Column("dept", "str"),
    Column("level", "int"),
    Column("rating", "float"),
]
SEED = 20260925
QUOTAS = {("eng",): 3, ("ops",): 2, ("lab",): 5}  # lab will be undersized


def make_rows():
    rows = []
    rid = 1
    for dept, n, base in (("eng", 8, 3), ("ops", 6, 2), ("lab", 2, 4)):
        for i in range(n):
            rating = None if i % 3 == 0 else base + i / 10.0
            rows.append((rid, dept, base + (i % 2), rating))
            rid += 1
    return rows


def show(title, result):
    print(f"\n== {title} ==")
    for g in result.groups:
        print(f"  group={g.group!r:10} quota={g.quota} actual={g.actual} "
              f"deficit={g.deficit} row_ids={list(g.row_ids)}")


def new_sampler():
    return GroupQuotaSampler(seed=SEED, columns=COLS, id_column="emp_id",
                             group_columns=("dept",), quotas=QUOTAS)


def main():
    rows = make_rows()

    # 1) batches: split must not matter
    sampler = new_sampler()
    for i in range(0, len(rows), 5):
        sampler.add_batch(TableBatch(COLS, rows[i:i + 5]), version=1)
    full = sampler.sample()
    show("full data, batches of 5", full)

    one_batch = new_sampler()
    one_batch.add_batch(TableBatch(COLS, rows), version=1)
    print("\nbatch-split invariant:", one_batch.sample() == full)

    # 2) shard merge (not concat-and-truncate)
    exports = []
    for shard in (rows[::2], rows[1::2]):
        s = new_sampler()
        s.add_batch(TableBatch(COLS, shard), version=1)
        exports.append(s.export_shard())
    merged = GroupQuotaSampler.merge_shards(exports)
    print("shard merge == full reference:", merged == full)

    # 3) versioned retraction: sampled row deleted -> replenished
    victim = full.groups[0].row_ids[0]
    sampler.retract([victim], version=2)
    repaired = sampler.sample()
    show(f"after retracting emp_id={victim} (v2)", repaired)
    remaining = [r for r in rows if r[0] != victim]
    ref = new_sampler()
    ref.add_batch(TableBatch(COLS, remaining), version=1)
    print("repaired == full reference on visible rows:", repaired == ref.sample())

    # 4) quota change matches an independent full run with the new quota
    changed = new_sampler()
    changed.add_batch(TableBatch(COLS, rows), version=1)
    for g in QUOTAS:
        changed.set_quota(g, 4)
    ref4 = new_sampler()
    for g in QUOTAS:
        ref4.set_quota(g, 4)
    ref4.add_batch(TableBatch(COLS, rows), version=1)
    print("quota change == independent full run:", changed.sample() == ref4.sample())

    # 5) checkpoint / restore
    state = json.loads(json.dumps(sampler.checkpoint()))
    restored = GroupQuotaSampler.restore(state)
    print("checkpoint restore == original:", restored.sample() == sampler.sample())


if __name__ == "__main__":
    main()
