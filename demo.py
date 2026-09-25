"""Runnable sample: typed predicate tree + block pruning + three-valued logic.

Run with:  python3.11 demo.py
"""

import math

from typedpred import (
    Batch,
    Col,
    Schema,
    Type,
    cross_check,
)

NAN = math.nan


def run(title, batch, predicate):
    print("=" * 72)
    print(title)
    print(f"  predicate: {predicate!r}")
    cols = ", ".join(
        f"{n}: {[v if not (isinstance(v, float) and math.isnan(v)) else 'NaN' for v in vals]}"
        for n, vals in batch.columns.items()
    )
    print(f"  block({batch.num_rows} rows): {cols}")
    result = cross_check(batch, predicate)
    for line in result.explain().splitlines():
        print("  " + line)
    print()


def main():
    int_schema = Schema({"x": Type.INT})
    float_schema = Schema({"x": Type.FLOAT})

    # 1) All-null block: x > 2 is provably empty (null -> UNKNOWN),
    #    but x IS NULL must NOT be skipped.
    all_null = Batch(int_schema, {"x": [None, None, None]})
    run("all-null block / x > 2  -> skip", all_null, Col("x") > 2)
    run("all-null block / x IS NULL -> scan, all rows match", all_null, Col("x").is_null())

    # 2) NaN != null. NaN must never contaminate min/max statistics:
    #    [NaN, 5.0, 1.0] must NOT be pruned for x > 2 (5.0 matches),
    #    regardless of the order NaN appears in.
    mixed_a = Batch(float_schema, {"x": [NAN, 5.0, 1.0]})
    mixed_b = Batch(float_schema, {"x": [5.0, 1.0, NAN]})
    run("NaN + matching value (NaN first) / x > 2", mixed_a, Col("x") > 2)
    run("NaN + matching value (NaN last)  / x > 2", mixed_b, Col("x") > 2)

    # [NaN, 1.0]: NaN > 2 is FALSE and max over valid values is 1 -> safe skip.
    no_match = Batch(float_schema, {"x": [NAN, 1.0]})
    run("NaN + low values / x > 2 -> safe skip", no_match, Col("x") > 2)

    # NaN satisfies != via IEEE-754; IS NULL is FALSE for NaN.
    run("NaN block / x IS NULL -> skip (NaN is not null)",
        Batch(float_schema, {"x": [NAN, NAN]}), Col("x").is_null())
    run("NaN block / x != 2 -> scan, NaN rows match",
        Batch(float_schema, {"x": [NAN, 1.0]}), Col("x").ne(2))

    # 3) Exact integer arithmetic with huge ints (no float coercion).
    big = 2 ** 80
    ints = Batch(int_schema, {"x": [big, big + 1, big - 3]})
    run("exact big integers / x = 2^80 + 1", ints, Col("x").eq(big + 1))

    # 4) NOT: NOT UNKNOWN is UNKNOWN. Null row is excluded; block with all
    #    values > 2 can be pruned under NOT(x > 2).
    with_nulls = Batch(int_schema, {"x": [5, None, 7]})
    run("mixed null block / NOT(x > 2) -> null row stays UNKNOWN, must scan",
        with_nulls, ~(Col("x") > 2))
    all_high = Batch(int_schema, {"x": [5, 7]})
    run("all-high block / NOT(x > 2) -> skip (operand always TRUE)",
        all_high, ~(Col("x") > 2))

    # 5) AND / OR pruning composes.
    run("AND: x > 2 AND x IS NULL -> skip (second branch empty)",
        Batch(int_schema, {"x": [1, 2]}),
        (Col("x") > 2) & Col("x").is_null())
    run("OR:  (x > 100) OR (x IS NULL) -> nulls may match, must scan",
        all_null, (Col("x") > 100) | Col("x").is_null())

    print("cross_check: every skip above was verified against the row "
          "interpreter (no TRUE rows).")


if __name__ == "__main__":
    main()
