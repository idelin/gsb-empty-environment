"""Run sample: synthetic local tables, three queries, pruning evidence.

    python3 demo.py
"""
from minirel import (
    Aggregate, And, Col, ColRef, Cmp, Filter, IsNull, Join, Lit, Project,
    Scan, Sort, Table, run,
)

# --- Synthetic base tables (local, no SQL engine) --------------------------
# NULL = None. Bags: duplicate rows are significant and preserved.
employees = Table(
    "employees",
    ["emp_id", "dept_id", "mgr_id", "name", "salary", "nickname"],
    [
        (1, 10, None, "amy", 90000, "a"),
        (2, 10, 1, "bob", 55000, None),
        (3, 20, 1, "cy", None, "c"),    # NULL salary
        (3, 20, 1, "cy", None, "c"),    # exact duplicate: multiset member
        (4, None, 2, "dan", 70000, "d"),  # NULL dept_id: never joins
        (5, 20, None, "eli", 48000, None),
    ],
)

departments = Table(
    "departments",
    ["dept_id", "dept_name", "budget", "location"],
    [
        (10, "eng", 1000000, "sf"),
        (20, "ops", None, "la"),        # NULL budget
        (30, "hr", 250000, "ny"),       # no employees
    ],
)


def show(title, schema, rows, evidence):
    print(f"\n### {title}")
    print(evidence.render())
    print("result schema:", [str(c) for c in schema])
    for row in rows:
        print("  ", row)
    print(f"  ({len(rows)} row(s))")


def query1():
    """Output has no sort column; salary still kept until after the sort."""
    e, d = Scan(employees, "e"), Scan(departments, "d")
    join = Join(e, d, Col(ColRef("e", "dept_id")), Col(ColRef("d", "dept_id")))
    filt = Filter(join, And(
        Cmp(">", Col(ColRef("e", "salary")), Lit(50000)),
        IsNull(Col(ColRef("d", "budget")), negated=True),
    ))
    sort = Sort(filt, [(Col(ColRef("e", "salary")), True)])  # DESC
    proj = Project(sort, [
        (Col(ColRef("e", "name")), "name"),
        (Col(ColRef("d", "dept_name")), "dept"),
    ])
    return run(proj)


def query2():
    """COUNT(*) over a filter: zero columns required, table must not vanish."""
    e = Scan(employees, "e")
    filt = Filter(e, Cmp(">", Col(ColRef("e", "salary")), Lit(50000)))
    agg = Aggregate(filt, aggs=[("count", None, "n")])  # COUNT(*)
    return run(agg)


def query3():
    """Self-join: same-named columns distinguished by source alias."""
    e, m = Scan(employees, "e"), Scan(employees, "m")
    join = Join(e, m, Col(ColRef("e", "mgr_id")), Col(ColRef("m", "emp_id")))
    proj = Project(join, [
        (Col(ColRef("e", "name")), "employee"),
        (Col(ColRef("m", "name")), "manager"),
    ])
    return run(proj)


if __name__ == "__main__":
    show("Q1: high earners, sorted by salary DESC (salary not in output)",
         *query1())
    show("Q2: COUNT(*) of employees earning > 50000 (no columns needed)",
         *query2())
    show("Q3: employee -> manager (self-join, e.name vs m.name)",
         *query3())
