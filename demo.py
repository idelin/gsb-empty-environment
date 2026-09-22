from __future__ import annotations

from relational import (
    Aggregate,
    AggExpr,
    ColRef,
    Column,
    Comparison,
    IsNull,
    Filter,
    Join,
    Limit,
    Literal,
    OrderTerm,
    Scan,
    Sort,
    optimize_query,
    sample_tables,
)


def show(title, plan, tables):
    relation = plan.rewritten.execute(tables)
    print(f"\n== {title} ==")
    print("rewritten:", type(plan.rewritten).__name__)
    for proof in plan.proofs:
        print(f"- {proof.rule}: {'SAFE' if proof.safe else 'RETAINED'} - {proof.reason}")
    print("rows:")
    for row in relation.rows:
        print(" ", row.values)


def main():
    tables = sample_tables()
    scores = Column("scores", "score")
    scores_id = Column("scores", "id")
    departments_id = Column("departments", "id")
    active = Column("departments", "active")
    department_id = Column("employees", "department_id")
    salary = Column("employees", "salary")

    score_plan = optimize_query(
        Limit(
            Sort(
                Scan("scores"),
                (OrderTerm(scores, descending=True), OrderTerm(scores_id)),
            ),
            5,
            3,
        ),
        tables,
    )
    show("ORDER BY score DESC, id OFFSET 5 LIMIT 3", score_plan, tables)

    safe_filter = Comparison("=", ColRef(active), Literal(True))
    join = Join(
        Scan("departments"),
        Scan("employees"),
        "left",
        (departments_id,),
        (department_id,),
        None,
    )
    filter_plan = optimize_query(Filter(join, safe_filter), tables)
    show("safe preserved-side predicate", filter_plan, tables)

    unsafe_filter = IsNull(ColRef(salary))
    unsafe_plan = optimize_query(Filter(join, unsafe_filter), tables)
    show("right predicate that is not null-rejecting is retained", unsafe_plan, tables)

    aggregate = Aggregate(
        join,
        (departments_id,),
        (
            AggExpr("count_star", None, Column("result", "rows")),
            AggExpr("sum", ColRef(salary), Column("result", "payroll")),
        ),
    )
    aggregate_plan = optimize_query(aggregate, tables)
    show("safe left-join aggregate pushdown", aggregate_plan, tables)


if __name__ == "__main__":
    main()
