from miniplanner import (
    Catalog,
    ColumnStats,
    Query,
    QueryPlanner,
    Table,
    and_,
    col,
    eq,
    is_null,
    lit,
    lt,
    or_,
    param,
)


def show(title, result):
    print(f"{title}: {result.plan_name}, cache_hit={result.cache_hit}, rows={result.rows}")


people = Table(
    "people",
    [
        {"id": 1, "name": "Ada", "age": 10},
        {"id": 2, "name": "Ada", "age": 10},
        {"id": 3, "name": "Cy", "age": 30},
        {"id": 4, "name": "Bo", "age": 40},
        {"id": 5, "name": "Nia", "age": None},
    ],
)
catalog = Catalog([people])
planner = QueryPlanner(catalog)

by_name = planner.prepare(Query("people", eq(col("name"), param(0)), ("id", "name")))
show("name=Ada (first execution)", by_name.execute(["Ada"]))
show("name=Cy  (same type/category, cached parameterized plan)", by_name.execute(["Cy"]))

young = planner.prepare(Query("people", lt(col("age"), param(0)), ("id", "age")))
show("age<15 (low-range estimate: index)", young.execute([15]))
show("age<35 (high-range estimate: full scan)", young.execute([35]))
show("age<NULL (top-level unknown: empty)", young.execute([None]))

null_or_true = planner.prepare(
    Query(
        "people",
        or_(eq(col("age"), param(0)), eq(col("age"), lit(30))),
        ("id", "age"),
    )
)
show("age=NULL OR age=30 (unknown OR true)", null_or_true.execute([None]))
show("age IS NULL", planner.prepare(Query("people", is_null(col("age")), ("id",))).execute([]))

changed_stats = people.with_stats(
    {
        "name": people.column_stats("name"),
        "age": ColumnStats(min_value=0, max_value=100, null_count=1, distinct_nonnull=4, value_type="int"),
    }
)
catalog.replace_table(changed_stats)
show("age<35 after stats change (invalidated and re-planned)", young.execute([35]))

stats = planner.cache.stats
print(
    "cache:",
    f"hits={stats.hits}",
    f"misses={stats.misses}",
    f"invalidations={stats.invalidations}",
    f"replans={stats.replans}",
)
