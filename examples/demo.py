"""Runnable demo of the minijoin engine.

Run from the repository root:

    python3.11 examples/demo.py
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from minijoin import NULL, Batch, ColType, Joiner, JoinType, Schema

EMP = Schema([("id", ColType.INT), ("dept", ColType.INT), ("name", ColType.STR)])
DEPT = Schema([("dept", ColType.INT), ("building", ColType.STR)])

# dept 10 appears 2x on the left and 3x on the right -> 6 output rows.
# NULL keys exist on both sides and never match each other.
employees = [
    (1, 10, "alice"),
    (2, 10, "bob"),
    (3, 20, "carol"),
    (4, NULL, "dave"),   # NULL key: kept by LEFT/FULL, never matched
    (5, 2**70 + 7, "eve"),  # exact big-int key
]
departments = [
    (10, "hq-a"),
    (10, "hq-b"),
    (10, "hq-c"),
    (NULL, "unassigned"),  # NULL key: kept by FULL, never matched
    (30, "annex"),
    (2**70 + 7, "skunkworks"),
]


def show(title, rows, schema):
    names = [c.name for c in schema.columns]
    print(f"\n== {title} ({len(rows)} rows) ==")
    print(" | ".join(names))
    for row in rows:
        print(" | ".join(repr(v) for v in row))


def main():
    left_batches = [Batch(EMP, employees[:2]), Batch(EMP, employees[2:])]
    right_batches = [Batch(DEPT, departments[:4]), Batch(DEPT, departments[4:])]

    for join_type in (JoinType.INNER, JoinType.LEFT, JoinType.FULL):
        joiner = Joiner(
            EMP,
            DEPT,
            left_keys=["dept"],
            right_keys=["dept"],
            join_type=join_type,
            # Tiny memory budget forces partitioned spill to disk, while
            # OutputOrder.LEFT (default) keeps the output order defined.
            memory_limit_rows=4,
            num_partitions=2,
        )
        rows = joiner.collect(left_batches, right_batches)
        show(f"{join_type.value} join (memory_limit_rows=4, spilled)", rows,
             joiner.output_schema)


if __name__ == "__main__":
    main()
