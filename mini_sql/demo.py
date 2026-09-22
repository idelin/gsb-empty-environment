from .sample_data import sample_database
from .planner import QueryPlanner


SAMPLES = (
    (
        "EXISTS -> SemiJoin; duplicate outer rows remain duplicate",
        """
        SELECT customers.name
        FROM customers
        WHERE EXISTS (
            SELECT *
            FROM payments
            WHERE payments.customer_id = customers.id
        )
        """,
    ),
    (
        "NOT EXISTS -> AntiJoin; customer 3 has no matching payment",
        """
        SELECT customers.name
        FROM customers
        WHERE NOT EXISTS (
            SELECT *
            FROM payments
            WHERE payments.customer_id = customers.id
        )
        """,
    ),
    (
        "COUNT and SUM -> GroupJoin; no match is 0 / NULL, NULL amount is skipped by both",
        """
        SELECT
            customers.name AS customer,
            (SELECT COUNT(*)
             FROM payments
             WHERE payments.customer_id = customers.id) AS payment_count,
            (SELECT COUNT(payments.amount)
             FROM payments
             WHERE payments.customer_id = customers.id) AS non_null_amount_count,
            (SELECT SUM(payments.amount)
             FROM payments
             WHERE payments.customer_id = customers.id) AS total_amount
        FROM customers
        """,
    ),
    (
        "Inner unqualified id shadows outer id",
        """
        SELECT boxes.box_id
        FROM boxes
        WHERE EXISTS (
            SELECT *
            FROM customers
            WHERE id = 2
              AND customers.id = boxes.customer_id
        )
        """,
    ),
)


def render_table(rows):
    if not rows:
        return "(no rows)"
    rendered = [[format_value(value) for value in row] for row in rows]
    widths = [max(len(row[index]) for row in rendered) for index in range(len(rendered[0]))]
    lines = []
    for row in rendered:
        lines.append(" | ".join(value.ljust(widths[index]) for index, value in enumerate(row)))
    return "\n".join(lines)


def format_value(value):
    if value is None:
        return "NULL"
    return str(value)


def run_demo():
    planner = QueryPlanner(sample_database())
    output = []
    for index, (title, sql) in enumerate(SAMPLES, start=1):
        plan = planner.plan(sql)
        rows = list(plan.execute(planner.database))
        output.append(f"{index}. {title}")
        output.append(sql.strip())
        output.append("Rewritten logical plan:")
        output.append(plan.explain())
        output.append("Result multiset:")
        output.append(render_table(rows))
        output.append("")
    return "\n".join(output)


if __name__ == "__main__":
    print(run_demo())
