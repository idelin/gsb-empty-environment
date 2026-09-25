"""Runnable demo: inner / left / full equi-joins with spill.

Run from the repo root:  python3 examples/demo.py
"""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from eqjoin import NULL, ColType, RecordBatch, join

INT, TEXT = ColType.INT, ColType.TEXT

EMP_SCHEMA = (("emp_id", INT), ("dept", INT), ("name", TEXT))
DEPT_SCHEMA = (("dept", INT), ("building", TEXT))

# Employees: dept 10 appears twice; one employee has NULL dept.
employees = [
    RecordBatch(EMP_SCHEMA, [(1, 10, "amy"), (2, 10, "bob"), (3, 20, "cy")]),
    RecordBatch(EMP_SCHEMA, [(4, NULL, "dan"), (5, 30, "eve")]),
]
# Departments: dept 10 appears three times; one department has NULL key.
departments = [
    RecordBatch(DEPT_SCHEMA, [(10, "HQ"), (10, "Lab"), (20, "Annex")]),
    RecordBatch(DEPT_SCHEMA, [(10, "HQ2"), (NULL, "Popup"), (40, "Gym")]),
]


def show(title, result):
    cols = [n for n, _ in result.schema]
    print(f"\n== {title} ==  ({len(result)} rows)")
    print("  " + " | ".join(cols))
    for row in result:
        print("  " + " | ".join(str(v) for v in row))
    print(f"  stats: {result.stats}")


def main():
    # Tiny memory budget (4 rows) forces partitioning + disk spill.
    kw = dict(left_on="dept", right_on="dept",
              num_partitions=2, max_rows_in_memory=4, order="left")

    show("INNER join (dept 10: 2 emp x 3 dept = 6 rows)",
         join(employees, departments, how="inner", **kw))
    show("LEFT join (NULL-dept employee kept)",
         join(employees, departments, how="left", **kw))
    show("FULL join (NULL keys kept on both sides)",
         join(employees, departments, how="full", **kw))

    # Pathological case: every key identical -> partition recursion would
    # never split; the engine falls back to block-nested-loop join.
    same_l = [RecordBatch((("k", INT),), [(7,)] * 25)]
    same_r = [RecordBatch((("k", INT),), [(7,)] * 25)]
    res = join(same_l, same_r, left_on="k", right_on="k", how="inner",
               num_partitions=2, max_rows_in_memory=4)
    print(f"\n== all keys identical: 25 x 25 ==  {len(res)} rows, "
          f"stats: {res.stats}")


if __name__ == "__main__":
    main()
