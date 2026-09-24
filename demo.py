"""Runnable sample for incremental_groupby (python3 demo.py)."""

import math

from incremental_groupby import FLOAT, INT, STR, Batch, GroupedAggregate


def show(title, table):
    print(f"\n== {title} ==")
    print(f"{'key':<8} {'count':>5} {'sum':>12} {'min':>12}")
    for r in table.result():
        key = "NULL" if r.key is None else str(r.key)
        total = "NULL" if r.sum is None else (
            "NaN" if isinstance(r.sum, float) and math.isnan(r.sum) else str(r.sum)
        )
        lo = "NULL" if r.min is None else str(r.min)
        print(f"{key:<8} {r.count:>5} {total:>12} {lo:>12}")


def main():
    schema = [("id", INT), ("key", STR), ("value", FLOAT)]
    t = GroupedAggregate(key_type=STR, value_type=FLOAT)

    # 1. Typed insert batch: null key, null value and NaN included.
    t.insert_batch(Batch(schema, [
        (1, "apple", 10.0),
        (2, "apple", 4.0),
        (3, "banana", 7.5),
        (4, None, 99.0),        # null key -> its own group
        (5, "banana", None),      # null value: counted, skipped by sum/min
        (6, "apple", float("nan")),  # NaN: a real value, unlike null
    ]))
    show("after insert batch", t)

    # 2. Atomic cross-group migration: row 1 moves apple -> banana.
    t.update_batch(Batch(schema, [(1, "banana", 10.0)]))
    show("row 1 migrated apple -> banana (no double count)", t)

    # 3. Delete the current min of "apple"; next min surfaces.
    t.delete_batch(Batch([("id", INT)], [(2,)]))
    show("deleted row 2 (apple min 4.0)", t)

    # 4. Remove NaN; apple sum becomes numeric again.
    t.delete_batch(Batch([("id", INT)], [(6,)]))
    show("deleted row 6 (NaN)", t)

    # 5. Delete remaining apple rows -> empty group reclaimed.
    t.delete_batch(Batch([("id", INT)], [(1,)]))
    show("deleted row 1; 'apple' group reclaimed", t)

    # 6. Exact big-integer arithmetic on an INT table.
    big = GroupedAggregate(key_type=STR, value_type=INT)
    big.insert_batch(Batch(
        [("id", INT), ("key", STR), ("value", INT)],
        [(1, "n", 10**30), (2, "n", 10**30), (3, "n", 7)],
    ))
    print(f"\n== exact integer sum ==\n10**30 * 2 + 7 = {big.get('n').sum}")

    # 7. Atomic batch: one bad op rejects the whole batch.
    before = t.result()
    try:
        t.apply_batch(Batch(
            [("op", STR), ("id", INT), ("key", STR), ("value", FLOAT)],
            [("I", 100, "cherry", 1.0), ("D", 999, None, None)],
        ))
    except KeyError as exc:
        print(f"\n== atomic batch rejected ==\n{exc!r}; state unchanged: {t.result() == before}")


if __name__ == "__main__":
    main()
