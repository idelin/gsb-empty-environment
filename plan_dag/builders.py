"""Programmatic plan builders for the sample queries."""

from __future__ import annotations

from plan_dag.expr import Arith, ColumnRef, Cmp, IsNull, Literal
from plan_dag.operators import (
    AggItem,
    AggregateSpec,
    FilterSpec,
    JoinSpec,
    LimitSpec,
    ProjectItem,
    ProjectSpec,
    ScanSpec,
)
from plan_dag.plan import Node, PlanDag


def scan(node_id: str, table: str) -> Node:
    return Node(node_id, ScanSpec(table), ())


def filt(node_id: str, child: str, predicate) -> Node:
    return Node(node_id, FilterSpec(predicate), (child,))


def project(node_id: str, child: str, items: list[tuple[str, object]]) -> Node:
    return Node(
        node_id,
        ProjectSpec([ProjectItem(name, expr) for name, expr in items]),
        (child,),
    )


def join(node_id: str, left: str, right: str, left_keys, right_keys,
         join_type: str = "inner") -> Node:
    return Node(
        node_id,
        JoinSpec(list(left_keys), list(right_keys), join_type=join_type),
        (left, right),
    )


def aggregate(node_id: str, child: str, aggs: list[tuple[str, object, str]],
              group_keys: list[object] | None = None) -> Node:
    items = [
        AggItem(func, None if arg is None else arg, alias)
        for func, arg, alias in aggs
    ]
    return Node(node_id, AggregateSpec(items, group_keys or []), (child,))


def limit(node_id: str, child: str, count: int) -> Node:
    return Node(node_id, LimitSpec(count), (child,))


def build_salary_report_plan() -> PlanDag:
    """Employees LEFT JOIN departments GROUP BY dname: count + total salary.

    The aggregate node is shared by a direct root and a limited root.
    """
    e = scan("emp_scan", "employees")
    d = scan("dept_scan", "departments")
    j = join(
        "emp_dept", "emp_scan", "dept_scan",
        left_keys=["dept"], right_keys=["dept"], join_type="left_outer",
    )
    agg = aggregate(
        "payroll_by_dept", "emp_dept",
        [
            ("count_star", None, "headcount"),
            ("sum", ColumnRef("salary"), "total_salary"),
            ("avg", ColumnRef("salary"), "avg_salary"),
        ],
        group_keys=[ColumnRef("dname")],
    )
    top2 = limit("top2", "payroll_by_dept", 2)
    return PlanDag([e, d, j, agg, top2], roots=["payroll_by_dept", "top2"])


def build_salary_over_60_plan() -> PlanDag:
    """Filter with NULL on salary (Fay) and NULL on active (Di): 3VL demo."""
    e = scan("s_emp", "employees")
    f = filt(
        "s_filt", "s_emp",
        Cmp("gt", ColumnRef("salary"), Literal(60)),
    )
    p = project(
        "s_proj", "s_filt",
        [("name", ColumnRef("name")), ("salary", ColumnRef("salary"))],
    )
    return PlanDag([e, f, p], roots=["s_proj"])


def build_type_mismatch_plan() -> PlanDag:
    """Compares TEXT salary against an INTEGER literal: rejected at bind time."""
    e = scan("m_emp", "employees")
    f = filt(
        "m_filt", "m_emp",
        Cmp("gt", ColumnRef("name"), Literal(10)),
    )
    return PlanDag([e, f], roots=["m_filt"])
