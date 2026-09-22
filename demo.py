"""Runnable sample: multiple ROWS windows sharing sorts over a synthetic table.

Run with:  python3.11 demo.py
"""

from window_sharing import (
    Frame,
    FrameBound,
    OrderKey,
    SortCounter,
    WindowSpec,
    execute_windows,
    partition_key_counts,
    physical_sort_count,
    naive_sort_count,
    render_plan,
)


# Local synthetic relation.  ``amount`` contains NULL on purpose so the
# NULLS FIRST / NULLS LAST distinction is observable.
SALES = [
    {"region": "East", "product": "bolt",  "amount": 10.0},
    {"region": "West", "product": "bolt",  "amount": None},
    {"region": "East", "product": "nut",   "amount": 10.0},  # duplicate sort key
    {"region": "East", "product": "bolt",  "amount": 20.0},
    {"region": "West", "product": "nut",   "amount": 30.0},
    {"region": "West", "product": "bolt",  "amount": 5.0},   # duplicate sort key
    {"region": "East", "product": "nut",   "amount": 10.0},  # duplicate sort key
    {"region": "West", "product": "nut",   "amount": 30.0},  # duplicate sort key
]


def windows():
    return [
        # Exact twins: same partition, same order, different ROWS frames.
        WindowSpec(
            "running_total",
            func="sum",
            partition_by=("region",),
            order_by=(OrderKey("product", ascending=True),
                      OrderKey("amount", ascending=True)),
            frame=Frame(FrameBound("UNBOUNDED PRECEDING"),
                        FrameBound("CURRENT ROW")),
            value_column="amount",
        ),
        WindowSpec(
            "centered_sum",
            func="sum",
            partition_by=("region",),
            order_by=(OrderKey("product", ascending=True),
                      OrderKey("amount", ascending=True)),
            frame=Frame(FrameBound("PRECEDING", 1),
                        FrameBound("FOLLOWING", 1)),
            value_column="amount",
        ),
        # Shorter prefix: product only.  It may reuse the (product, amount)
        # sort above - but still computes its own whole-partition frame.
        WindowSpec(
            "product_rank",
            func="rank",
            partition_by=("region",),
            order_by=(OrderKey("product", ascending=True),),
        ),
        # Descending: cannot reuse the ascending sort.
        WindowSpec(
            "amount_rank_desc",
            func="rank",
            partition_by=("region",),
            order_by=(OrderKey("amount", ascending=False),),  # NULLS FIRST default
        ),
        # Same direction as above but NULLS LAST: not directly reusable.
        WindowSpec(
            "amount_rank_desc_nullslast",
            func="rank",
            partition_by=("region",),
            order_by=(OrderKey("amount", ascending=False, nulls_first=False),),
        ),
        # No PARTITION BY, no ORDER BY: a distinct global sort shape.
        WindowSpec("grand_count", func="count"),
    ]


def main() -> None:
    specs = windows()
    counter = SortCounter()
    results, plan = execute_windows(SALES, specs, counter=counter)
    counts = partition_key_counts(SALES, plan)

    print("Input relation SALES (multiset; duplicates kept):")
    print("  idx | region | product | amount")
    for index, row in enumerate(SALES):
        print(f"  {index:>3} | {row['region']:<6} | {row['product']:<7} | "
              f"{'NULL' if row['amount'] is None else row['amount']}")
    print()
    print(render_plan(plan, counts))
    print()
    print("Actual sort() calls executed by the shared plan:", counter.count)
    print("Predicted from plan shape x partitions:         ",
          physical_sort_count(plan, counts))
    print("Non-sharing baseline (one sort per window):     ",
          naive_sort_count(specs, counts))
    print()
    print("Results (aligned to input order; each window has its own array):")
    header = "  idx | " + " | ".join(f"{result.name:<28}" for result in results)
    print(header)
    for index in range(len(SALES)):
        cells = " | ".join(f"{str(result.values[index]):<28}" for result in results)
        print(f"  {index:>3} | {cells}")
    print()
    print("Notes:")
    print("  * running_total and centered_sum share S1 but keep separate frames/results.")
    print("  * product_rank reuses S1 as a prefix (extra 'amount' key only breaks ties).")
    print("  * amount_rank_desc and amount_rank_desc_nullslast cannot share: NULL placement differs.")
    print("  * ASC vs DESC sorts are never reused; no reverse-scan assumption is made.")
    print("  * Duplicate rows survive: multiset semantics, no DISTINCT anywhere.")


if __name__ == "__main__":
    main()
