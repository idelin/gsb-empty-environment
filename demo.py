"""Runnable tour of the incremental grouped aggregation table."""

from grouped_agg import Batch, ColType, GroupedAggregate


def show(title, agg):
    print(f"\n== {title} ==")
    rows = agg.result()
    if not rows:
        print("  (empty)")
    for r in rows:
        print(f"  key={r.key!r:8} count={r.count:<3} sum={r.sum!r:<24} min={r.min!r}")


def main():
    agg = GroupedAggregate(key_type=ColType.STR, value_type=ColType.INT)

    # 1. Typed batch input (id / key / value columns).
    batch = Batch.from_rows(
        {"id": ColType.INT, "key": ColType.STR, "value": ColType.INT},
        [
            (1, "A", 10),
            (2, "A", 20),
            (3, "B", 5),
            (4, "B", 7),
            (5, None, 100),   # NULL group key
            (6, "C", None),   # NULL value
            (7, "C", None),
        ],
    )
    agg.insert_batch(batch)
    show("after inserting typed batch", agg)

    # 2. Atomic cross-group migration: row 1 moves A -> B, never double counted.
    agg.update(1, key="B")
    show("row 1 migrated A -> B (atomic, no double count)", agg)

    # 3. Deleting the current minimum exposes the next-smallest value.
    agg.delete(3)  # B's min was 5
    show("deleted B's minimum (5) -> next min surfaces", agg)

    # 4. Empty groups are reclaimed: C has only NULL values but 2 rows;
    #    deleting both removes the group entirely.
    agg.delete(6)
    agg.delete(7)
    show("group C reclaimed after its last row left", agg)

    # 5. All-NULL group: sum is NULL, count(*) still counts rows.
    agg.insert(8, "D", None)
    agg.insert(9, "D", None)
    show("group D with only NULL values (sum=NULL, count=2)", agg)

    # 6. Exact big-integer arithmetic.
    agg.insert(10, "E", 10**30 + 1)
    agg.insert(11, "E", 10**30 + 2)
    show("exact 31-digit integer sum", agg)

    # 7. NaN is not NULL (float column).
    fagg = GroupedAggregate(key_type=ColType.STR, value_type=ColType.FLOAT)
    fagg.insert(1, "F", float("nan"))
    fagg.insert(2, "F", 2.5)
    show("NaN row counts toward count/sum but not min", fagg)
    fagg.delete(1)
    show("NaN deleted -> sum recovers to 2.5", fagg)

    # 8. Atomic multi-op batch: a failing op aborts the whole batch.
    try:
        agg.apply_ops([
            ("insert", 12, "A", 1),
            ("insert", 13, "A", "oops"),  # type error -> nothing applied
        ])
    except TypeError as exc:
        print(f"\n== rejected batch stays atomic ==\n  {exc}")
    show("state unchanged after failed batch", agg)


if __name__ == "__main__":
    main()
