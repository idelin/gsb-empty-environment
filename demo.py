"""Run samples for distagg: two-phase aggregation over synthesized tables."""

from distagg import (
    Avg,
    Count,
    CountStar,
    Max,
    Min,
    Sum,
    Table,
    direct_aggregate,
    distributed_aggregate,
    local_aggregate,
)


def show(title):
    print(f"\n=== {title} ===")


def main():
    show("Synthesized table (multiset: duplicate rows are real rows)")
    employees = Table(
        ("id", "dept", "salary", "bonus"),
        [
            (1, "eng", 100, 10),
            (2, "eng", 200, None),
            (3, "ops", None, 30),
            (4, "ops", 200, 30),
            (5, None, 300, None),
            (6, "eng", 200, None),
            (6, "eng", 200, None),  # duplicated row on purpose
        ],
    )
    print("columns:", employees.columns)
    for row in employees.rows:
        print("  ", row)

    show("Local phase: each shard folds rows into intermediate states")
    shards = employees.shard(3) + [Table(employees.columns, [])]  # + empty shard
    aggs = [
        CountStar(),
        Count("bonus"),
        Sum("salary"),
        Avg("salary"),
        Count("dept", distinct=True),
    ]
    for i, shard in enumerate(shards):
        states = local_aggregate(shard, aggs)
        print(f"shard {i} ({len(shard)} rows):")
        for agg, state in zip(aggs, states):
            print(f"  {agg.label():<22} state = {state}")

    show("Global phase: merge states and finalize")
    results = distributed_aggregate(shards, aggs)
    for agg, value in zip(aggs, results):
        print(f"  {agg.label():<22} = {value}")

    show("AVG pitfall: averaging shard averages is wrong")
    skewed = Table(("x",), [(100,)] + [(1,)] * 9)
    skewed_shards = skewed.shard(3)  # unequal shard sizes
    shard_means = [
        direct_aggregate(s, [Avg("x")])[0] for s in skewed_shards if len(s)
    ]
    naive = sum(shard_means) / len(shard_means)
    correct = distributed_aggregate(skewed_shards, [Avg("x")])[0]
    print(f"  shard means           = {shard_means}")
    print(f"  mean of shard means   = {naive}   <- WRONG")
    print(f"  sum/count over shards = {correct}   <- correct")

    show("Empty shard contributes nothing to the AVG denominator")
    full = Table(("x",), [(10,), (20,)])
    empty = Table(("x",), [])
    avg = distributed_aggregate([full, empty], [Avg("x")])[0]
    print(f"  AVG over [full, empty] = {avg}  (30/2, not 30/3)")

    show("NULL semantics")
    all_null = Table(("x",), [(None,), (None,), (None,)])
    print(f"  SUM over all-NULL column = "
          f"{direct_aggregate(all_null, [Sum('x')])[0]}")
    print(f"  COUNT(x) all-NULL column = "
          f"{direct_aggregate(all_null, [Count('x')])[0]}")
    print(f"  COUNT(*) all-NULL column = "
          f"{direct_aggregate(all_null, [CountStar()])[0]}")
    empty_table = Table(("x",), [])
    print("  empty table: "
          f"COUNT(*)={direct_aggregate(empty_table, [CountStar()])[0]}, "
          f"SUM={direct_aggregate(empty_table, [Sum('x')])[0]}, "
          f"AVG={direct_aggregate(empty_table, [Avg('x')])[0]}, "
          f"MIN={direct_aggregate(empty_table, [Min('x')])[0]}, "
          f"MAX={direct_aggregate(empty_table, [Max('x')])[0]}")

    show("DISTINCT aggregates deduplicate across shards")
    t = Table(("x",), [(10,), (10,), (20,), (None,)])
    print(f"  rows: {t.rows}")
    print(f"  SUM(x)            = "
          f"{distributed_aggregate(t.shard(2), [Sum('x')])[0]}")
    print(f"  SUM(DISTINCT x)   = "
          f"{distributed_aggregate(t.shard(2), [Sum('x', distinct=True)])[0]}")
    print(f"  COUNT(DISTINCT x) = "
          f"{distributed_aggregate(t.shard(2), [Count('x', distinct=True)])[0]}")


if __name__ == "__main__":
    main()
