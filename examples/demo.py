"""Runnable sample: python3 examples/demo.py"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from vecplan import (FLOAT64, INT32, INT64, STRING, ErrorPolicy,
                     ExecutionError, RecordBatch, cast, col, compile, if_,
                     is_null, lit)


def show(label, vector):
    print(f"{label:<38} -> {vector.values}")


def main():
    schema = {"a": INT32, "b": INT32, "f": FLOAT64, "s": STRING}
    batch = RecordBatch(schema, {
        "a": [10, 20, None, 2 ** 31 - 1],
        "b": [3, 0, 5, 0],
        "f": [1.5, float("nan"), None, 2.5],
        "s": ["x", "y", "z", "w"],
    })
    print("input batch (typed columns):")
    for name, typ in schema.items():
        print(f"  {name} {typ}: {batch.columns[name]}")
    print()

    show("a + b", compile(col("a") + col("b"), schema).run(batch))

    # Short-circuit: rows with b == 0 never touch the division.
    safe_div = if_(col("b") != 0, col("a") / col("b"), lit(-1))
    show("if(b != 0, a / b, -1)", compile(safe_div, schema).run(batch))

    # The unselected branch is never evaluated at all.
    classic = if_(lit(False), lit(1) / lit(0), lit(7))
    show("if(false, 1 / 0, 7)", compile(classic, schema).run(batch))

    # Integer overflow is reported per error policy, with exact rows.
    overflow = compile(col("a") + col("a"), schema)
    try:
        overflow.run(batch, ErrorPolicy.STRICT)
    except ExecutionError as exc:
        print(f"strict a + a raises: {exc} (rows={exc.rows})")
    show("a + a with ErrorPolicy.NULL", overflow.run(batch, ErrorPolicy.NULL))

    # null comparison yields unknown (None), not false.
    show("a = 10  (null -> unknown)", compile(col("a") == 10, schema).run(batch))

    # NaN is a value; null is not. They are different things.
    show("is_null(f)  (NaN is not null)", compile(is_null(col("f")), schema).run(batch))
    show("f = f  (NaN != NaN)", compile(col("f") == col("f"), schema).run(batch))

    # Integer arithmetic is exact beyond float53 precision.
    big = RecordBatch({"x": INT64}, {"x": [2 ** 53 + 1]})
    show("int64: (2^53 + 1) + 1", compile(col("x") + 1, big.schema).run(big))

    # Explicit casts, with per-row errors under the STRICT policy.
    f_to_int = compile(cast(col("f"), INT32), schema)
    try:
        f_to_int.run(batch, ErrorPolicy.STRICT)
    except ExecutionError as exc:
        print(f"strict cast(f as INT32) raises: {exc} (rows={exc.rows})")
    show("cast(f as INT32), NULL policy", f_to_int.run(batch, ErrorPolicy.NULL))
    show("cast(a as STRING)", compile(cast(col("a"), STRING), schema).run(batch))


if __name__ == "__main__":
    main()
