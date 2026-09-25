"""End-to-end demo: compile expressions, run them over typed batches.

Run with:  python3.11 examples/demo.py
"""

import math
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from vector_expr import (  # noqa: E402
    INT64_MAX,
    Batch,
    Column,
    CompileError,
    EvaluationError,
    Type,
    compile_expression,
)

SCHEMA = {
    "id": Type.INT64,
    "salary": Type.INT64,
    "bonus_pct": Type.FLOAT64,
    "active": Type.BOOL,
}

BATCH = Batch({
    "id": Column(Type.INT64, [1, 2, 3, 4]),
    "salary": Column(Type.INT64, [1000, 2500, None, 4000]),
    "bonus_pct": Column(Type.FLOAT64, [0.10, float("nan"), 0.25, None]),
    "active": Column(Type.BOOL, [True, False, True, None]),
})


def show(expr, batch=BATCH, schema=SCHEMA):
    plan = compile_expression(expr, schema)
    result = plan.execute(batch)
    print(f"expr: {expr}")
    print(f"  type:   {result.type}")
    print(f"  values: {result.values}")
    return result.values


def main():
    print("== input batch ==")
    for name, col in BATCH.columns.items():
        print(f"  {name:9s} {col.type.value:8s} {col.values}")
    print()

    print("== arithmetic / comparison / logic ==")
    show("salary + 500")
    show("salary * 2 > 3000")
    show("active AND salary > 1500")
    show("CAST(salary AS FLOAT64) * bonus_pct")
    print()

    print("== null vs NaN (row 1 bonus is NaN, row 3 bonus is null) ==")
    show("bonus_pct = bonus_pct")
    print()

    print("== short-circuit IF ==")
    show("if(false, 1/0, 7)")
    show("if(active, salary / 2, 0)")
    print()

    print("== errors ==")
    try:
        compile_expression("salary + 0.5", SCHEMA)
    except CompileError as exc:
        print(f"compile-time type error (before any execution):\n  {exc}")
    print()
    try:
        plan = compile_expression("salary + 1", SCHEMA)
        bad = Batch({"salary": Column(Type.INT64, [10, INT64_MAX, 30])})
        plan.execute(bad)
    except EvaluationError as exc:
        print(f"integer overflow reported with row:\n  {exc}")
    print()
    try:
        plan = compile_expression("if(active, 100 / id, 0)",
                                  {"active": Type.BOOL, "id": Type.INT64})
        plan.execute(Batch({
            "active": Column(Type.BOOL, [False, True]),
            "id": Column(Type.INT64, [0, 0]),
        }))
    except EvaluationError as exc:
        print(f"division by zero only fires on selected rows:\n  {exc}")
    print()
    try:
        plan = compile_expression("CAST(bonus_pct AS INT64)",
                                  {"bonus_pct": Type.FLOAT64})
        plan.execute(Batch({
            "bonus_pct": Column(Type.FLOAT64, [1.5, math.nan]),
        }))
    except EvaluationError as exc:
        print(f"NaN -> INT64 cast error with row:\n  {exc}")


if __name__ == "__main__":
    main()
