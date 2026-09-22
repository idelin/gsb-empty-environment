"""Runnable sample: shared-subplan materialize/recompute decisions.

Run with:  python3.11 demo.py
"""
from __future__ import annotations

import relalg
from decision import CostModel
from engine import Executor
from materialize import Catalog
from plan import Filter, Join, Project, Scan, register_predicate

# --- synthetic local tables (NULLs and duplicate rows on purpose) ----------
departments = relalg.Table(
    "departments",
    ("dept_id", "dept_name"),
    [
        (1, "eng"),
        (2, "sales"),
        (3, "ops"),
        (None, "classified"),   # NULL key: never joins
    ],
)

employees = relalg.Table(
    "employees",
    ("emp_id", "name", "dept_id", "salary"),
    [
        (1, "ada",   1,    90000),
        (2, "alan",  1,    55000),
        (3, "grace", 2,    40000),
        (4, "edsger", None, 70000),   # NULL dept: excluded from the join
        (5, "barbara", 3,   None),    # NULL salary: UNKNOWN in comparisons
        (3, "grace", 2,    40000),    # duplicate row: multiset semantics
    ],
)

register_predicate("salary_over_50k",
                   lambda r: relalg.gt(r["salary"], 50000))
register_predicate("dept_is_eng",
                   lambda r: relalg.eq(r["dept_name"], "eng"))
register_predicate("dept_is_sales",
                   lambda r: relalg.eq(r["dept_name"], "sales"))
register_predicate("big_even_key",
                   lambda r: relalg.eq(r["k"] % 2, 0))
register_predicate("big_value_pos",
                   lambda r: relalg.gt(r["v"], 0))


def show(title, rows):
    print(f"  {title} ({len(rows)} rows)")
    for row in rows:
        print(f"    {row}")


def main():
    print("=" * 72)
    print("1) Two consumers, different filters, one shared join subplan")
    print("=" * 72)
    shared_join = Join(Scan("employees"), Scan("departments"),
                       ("dept_id",), ("dept_id",))
    q_high_earners = Project(("name", "salary", "dept_name"),
                             Filter("salary_over_50k", shared_join))
    q_eng_only = Project(("name", "dept_name"),
                         Filter("dept_is_eng", shared_join))

    ex = Executor([employees, departments])
    res_high, res_eng = ex.run_all([q_high_earners, q_eng_only])
    for node, d in ex.decision_log:
        print(f"  [decision] {type(node).__name__:<8} -> {d.strategy}")
        print(f"             {d.rationale}")
    show("Q1 salary > 50000", res_high)
    show("Q2 dept = eng", res_eng)
    join_view = ex.catalog.get(shared_join.key())
    print(f"  shared join materialized once, {len(join_view.read())} rows "
          f"(unfiltered), spilled={join_view.spilled}")

    print()
    print("=" * 72)
    print("2) Single-use subplan: no gratuitous materialization")
    print("=" * 72)
    q_single = Filter("dept_is_sales",
                      Join(Scan("employees"), Scan("departments"),
                           ("dept_id",), ("dept_id",)))
    ex2 = Executor([employees, departments])
    res = ex2.run(q_single)
    for node, d in ex2.decision_log:
        print(f"  [decision] {type(node).__name__:<8} -> {d.strategy}")
        print(f"             {d.rationale}")
    print(f"  catalog entries after run: {len(ex2.catalog.keys())}")
    show("sales employees", res)

    print()
    print("=" * 72)
    print("3) Memory quota exceeded: shared subplan spills to disk")
    print("=" * 72)
    big = relalg.Table(
        "big", ("k", "v"),
        [(i, i * 7 % 13 - 6) for i in range(2000)],
    )
    shared_filter = Filter("big_even_key", Scan("big"), selectivity=0.5)
    q_a = Project(("k",), shared_filter)
    q_b = Filter("big_value_pos", shared_filter)
    small_quota = CostModel(mem_quota_bytes=4096)  # 64 rows worth
    ex3 = Executor([big], model=small_quota)
    res_a, res_b = ex3.run_all([q_a, q_b])
    for node, d in ex3.decision_log:
        print(f"  [decision] {type(node).__name__:<8} -> {d.strategy}")
        print(f"             {d.rationale}")
    view = ex3.catalog.get(shared_filter.key())
    print(f"  view spilled={view.spilled}, rows={len(view.read())}, "
          f"Q1 rows={len(res_a)}, Q2 rows={len(res_b)}")


if __name__ == "__main__":
    main()
