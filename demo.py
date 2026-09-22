"""Runnable sample:  python3.11 demo.py

View maintained incrementally (no SQL engine involved):

    SELECT e.dept, d.building, COUNT(*), SUM(e.salary)
    FROM employees e JOIN departments d ON e.dept = d.dept
    WHERE e.age >= 30
    GROUP BY e.dept, d.building
"""

from ivm import (
    NULL, BaseTable, Filter, GroupBy, Join, NegativeMultiplicityError,
    Project, View, sql_ge,
)
from sample_data import (
    DEPARTMENT_SCHEMA, DEPARTMENTS, EMPLOYEE_SCHEMA, EMPLOYEES,
    EMPLOYEES_BATCH2,
)


def build():
    """Wire up the operator DAG and return (employees, departments, view)."""
    emp = BaseTable("employees", EMPLOYEE_SCHEMA)
    dept = BaseTable("departments", DEPARTMENT_SCHEMA)
    # joined row: (name, dept, age, salary, d.dept, building)
    join = Join("emp_join_dept", emp, dept, left_keys=(1,), right_keys=(0,))
    adult = join.connect(Filter("age>=30", lambda r: sql_ge(r[2], 30)))
    proj = adult.connect(Project("dept,building,salary", (1, 5, 3)))
    grp = proj.connect(GroupBy(
        "by_dept", keys=(0, 1), aggs=(("count", None), ("sum", 2))))
    view = grp.connect(View("result"))
    return emp, dept, view


def show_deltas(view, offset):
    for row, count in view.delta_log[offset:]:
        sign = "+" if count > 0 else ""
        print(f"    delta {sign}{count} x {row}")
    return len(view.delta_log)


def show_view(view):
    if not view.bag:
        print("    <empty>")
    for row, n in view.items():
        print(f"    {n} x {row}")


def main():
    emp, dept, view = build()
    offset = 0

    print("== batch 1: load departments + first employees ==")
    dept.apply([(rid, +1, row) for rid, row in DEPARTMENTS])
    emp.apply([(rid, +1, row) for rid, row in EMPLOYEES])
    offset = show_deltas(view, offset)
    print("  view now:")
    show_view(view)

    print("== batch 2: more employees (duplicates, NULL salary) ==")
    emp.apply([(rid, +1, row) for rid, row in EMPLOYEES_BATCH2])
    offset = show_deltas(view, offset)
    print("  view now:")
    show_view(view)

    print("== batch 3: retractions ==")
    emp.delete("e6")   # ops group drops back to multiplicity 1
    print("  after delete e6:")
    offset = show_deltas(view, offset)
    emp.delete("e3")   # last supporting ops row -> group row deleted
    print("  after delete e3 (last ops supporter):")
    offset = show_deltas(view, offset)
    emp.delete("e1")
    emp.delete("e5")   # eng group empties -> view becomes empty
    print("  after delete e1, e5:")
    offset = show_deltas(view, offset)
    print("  view now:")
    show_view(view)

    print("== negative multiplicity is an error ==")
    try:
        # a retraction for a tuple that is not present downstream
        view.receive([(("ghost", "X", 1, 1), -1)], None)
    except NegativeMultiplicityError as exc:
        print(f"    raised NegativeMultiplicityError: {exc}")

    print("== batch reorder converges to the same final view ==")
    batches = [
        lambda e, d: d.apply([(rid, +1, row) for rid, row in DEPARTMENTS]),
        lambda e, d: e.apply([(rid, +1, row) for rid, row in EMPLOYEES]),
        lambda e, d: e.apply([(rid, +1, row) for rid, row in EMPLOYEES_BATCH2]),
        lambda e, d: [e.delete(rid) for rid in ("e6", "e3")],
    ]
    finals = []
    for order in ((0, 1, 2, 3), (1, 0, 2, 3), (2, 1, 0, 3)):
        e, d, v = build()
        for i in order:
            batches[i](e, d)
        finals.append(v.bag)
        print(f"    order {order}: final = {v.items()}")
    print(f"    all orders agree: {finals[0] == finals[1] == finals[2]}")


if __name__ == "__main__":
    main()
