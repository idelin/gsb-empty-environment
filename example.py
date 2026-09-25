"""End-to-end demo of detsample.  Run: python3.11 example.py"""

from detsample import Batch, Column, Sampler

SEED = 42
COLS = [
    Column("id", "int"),
    Column("grp", "str"),
    Column("score", "float"),   # may hold null (None) or NaN -- kept distinct
    Column("flag", "bool"),
]
QUOTAS = {"apple": 3, "banana": 3, "cherry": 5}  # cherry will be short


def make_batch(rows):
    return Batch(COLS, rows)


def show(title, results):
    print(f"\n== {title} ==")
    for r in results:
        print(f"  group={r.group!r:10} quota={r.quota} actual={r.actual} "
              f"shortfall={r.shortfall} row_ids={list(r.row_ids)}")


def main():
    nan = float("nan")
    rows = (
        [(i, "apple", i * 1.5, i % 2 == 0) for i in range(10)]
        + [(100 + i, "banana", None if i % 3 else nan, True) for i in range(8)]
        + [(200 + i, "cherry", 0.5 * i, False) for i in range(2)]  # small group
    )

    # 1) Full-data reference: one pass over all rows.
    ref = Sampler(SEED, QUOTAS)
    ref.add_batch(make_batch(rows), id_col="id", group_col="grp")
    show("full-data reference", ref.sample())

    # 2) Sharded processing: split rows into 3 shards, export, merge.
    #    Merge takes the k smallest keys of the UNION (never concat+truncate).
    shards = []
    for i in range(3):
        part = Sampler(SEED, QUOTAS)
        part.add_batch(make_batch(rows[i::3]), id_col="id", group_col="grp")
        shards.append(part.export_shard())
    merged = Sampler.from_shards(SEED, QUOTAS, shards)
    show("merged from 3 shards", merged.sample())
    assert [r.row_ids for r in merged.sample()] == [r.row_ids for r in ref.sample()]
    print("  -> identical to full-data reference")

    # 3) Versioned retraction: retract a sampled row, sample is replenished.
    victim = merged.sample()[0].row_ids[0]
    merged.retract(victim, version=0)
    show(f"after retracting row {victim} (v0)", merged.sample())
    ref.retract(victim, version=0)
    assert [r.row_ids for r in merged.sample()] == [r.row_ids for r in ref.sample()]
    print("  -> replenished from candidates, still identical to reference")

    # 4) A newer version of the retracted row becomes visible again.
    merged.add_row(victim, "apple", values=(victim, "apple", 9.9, True), version=1)
    show(f"after row {victim} v1 arrives", merged.sample())

    # 5) Checkpoint / restore: output equals the independent full reference.
    state = merged.checkpoint()
    restored = Sampler.restore(state)
    restored.add_batch(make_batch([(300, "apple", 1.0, False)]),
                       id_col="id", group_col="grp")
    ref2 = Sampler(SEED, QUOTAS)
    ref2.add_batch(make_batch(rows), id_col="id", group_col="grp")
    ref2.retract(victim, version=0)
    ref2.add_row(victim, "apple", values=(victim, "apple", 9.9, True), version=1)
    ref2.add_row(300, "apple", values=(300, "apple", 1.0, False))
    show("restored from checkpoint + 1 new row", restored.sample())
    assert [r.row_ids for r in restored.sample()] == [r.row_ids for r in ref2.sample()]
    print("  -> identical to independent full-data reference")

    # 6) Quota change is just a re-read of the retained candidates.
    restored.set_quota("banana", 0)
    show("after set_quota('banana', 0)", restored.sample())


if __name__ == "__main__":
    main()
