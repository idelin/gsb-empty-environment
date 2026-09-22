#!/usr/bin/env python3
"""Runnable sample: serialize -> deserialize -> validate -> execute a plan DAG.

Run with:  python3.11 demo.py
Standard library only. No SQL engine is used or imported.
"""

from __future__ import annotations

import json

from plan_dag import (
    AggregateNode,
    Catalog,
    FilterNode,
    HashJoinNode,
    NamedExpr,
    PlanDagError,
    ProjectNode,
    ScanNode,
    Schema,
    Table,
    binop,
    build_plan,
    col,
    execute,
    from_document,
    lit,
)
from plan_dag.expr import IsNullExpr
from plan_dag.types import NULL


def build_catalog() -> Catalog:
    customers = Table.create(
        Schema.of(("cid", "INT"), ("name", "TEXT")),
        [(1, "Ann"), (2, "Bo"), (3, "Cy")],
    )
    orders = Table.create(
        Schema.of(("oid", "INT"), ("cid", "INT"), ("region", "TEXT"), ("amount", "INT")),
        [
            (10, 1, "east", 100),
            (11, 1, "east", None),     # NULL amount
            (12, 2, "west", 50),
            (13, 2, "west", 50),       # duplicate row -> multiset
            (14, 3, "west", 70),
        ],
    )
    return Catalog({"customers": customers, "orders": orders})


def build_demo_plan(catalog: Catalog):
    scan_customers = ScanNode(id="scan_customers", table="customers")
    scan_orders = ScanNode(id="scan_orders", table="orders")

    # Shared by two aggregates: after a serialize/deserialize round trip this
    # node is still a single node that is evaluated once.
    open_orders = FilterNode(
        id="open_orders",
        inputs=["scan_orders"],
        predicate=binop("and", binop(">=", col("amount"), lit(50)),
                        IsNullExpr(col("amount"), negated=True)),
    )

    count_by_customer = AggregateNode(
        id="count_by_customer",
        inputs=["open_orders"],
        group_keys=("cid",),
        aggregates=({"func": "count", "alias": "n_orders", "expr": {"e": "star"}},),
    )
    total_by_customer = AggregateNode(
        id="total_by_customer",
        inputs=["open_orders"],
        group_keys=("cid",),
        aggregates=({
            "func": "sum",
            "alias": "total_amount",
            "expr": {"e": "col", "name": "amount"},
        },),
    )
    aggregates_join = HashJoinNode(
        id="aggregates_join",
        inputs=["count_by_customer", "total_by_customer"],
        left_keys=("cid",),
        right_keys=("cid",),
    )
    customer_join = HashJoinNode(
        id="customer_join",
        inputs=["scan_customers", "aggregates_join"],
        left_keys=("cid",),
        right_keys=("cid",),
    )
    root = ProjectNode(
        id="root",
        inputs=["customer_join"],
        items=[
            NamedExpr("customer", col("name")),
            NamedExpr("orders", col("n_orders")),
            NamedExpr("total", col("total_amount")),
        ],
    )
    nodes = [
        scan_customers,
        scan_orders,
        open_orders,
        count_by_customer,
        total_by_customer,
        aggregates_join,
        customer_join,
        root,
    ]
    return build_plan("root", nodes, catalog)


def show_relation(relation) -> None:
    header = " | ".join(f"{column.name}:{column.data_type}" for column in relation.schema.columns)
    print(header)
    print("-" * len(header))
    for row in relation.rows:
        print(" | ".join("NULL" if value is NULL else str(value) for value in row))


def show_rejection(title: str, document: dict, catalog: Catalog) -> None:
    print(f"[reject] {title}")
    try:
        from_document(document, catalog)
    except PlanDagError as exc:
        print(f"         {type(exc).__name__}: {exc}")
    else:
        raise AssertionError("invalid document was accepted")


def main() -> None:
    catalog = build_catalog()

    print("=== 1. Build, serialize and inspect the typed/versioned document ===")
    plan = build_demo_plan(catalog)
    document = plan.to_document()
    print(json.dumps(document, indent=2, ensure_ascii=False))

    print("\n=== 2. Deserialize (checks types/fields/cycles) and execute ===")
    restored = from_document(document, catalog)
    relation, stats = execute(restored, catalog)
    show_relation(relation)

    print("\n=== 3. Shared subtree stays shared after round trip ===")
    print("per-node evaluation counts:", dict(stats.evaluations))
    print("open_orders evaluated:", stats.evaluations["open_orders"], "time(s)")

    print("\n=== 4. Invalid documents are rejected before execution ===")
    valid = document

    unknown_op = json.loads(json.dumps(valid))
    next(node for node in unknown_op["nodes"] if node["id"] == "open_orders")["op"] = "os.system"
    show_rejection("unknown operator (embedded code never runs)", unknown_op, catalog)

    cyclic = json.loads(json.dumps(valid))
    next(node for node in cyclic["nodes"] if node["id"] == "scan_orders")["inputs"] = ["open_orders"]
    show_rejection("cycle", cyclic, catalog)

    missing = json.loads(json.dumps(valid))
    next(node for node in missing["nodes"] if node["id"] == "open_orders")["inputs"] = ["ghost"]
    show_rejection("edge to missing node", missing, catalog)

    conflict = json.loads(json.dumps(valid))
    duplicate = json.loads(json.dumps(next(node for node in conflict["nodes"] if node["id"] == "scan_orders")))
    duplicate["table"] = "customers"
    conflict["nodes"].append(duplicate)
    show_rejection("same id, different content", conflict, catalog)

    wrong_type = json.loads(json.dumps(valid))
    next(node for node in wrong_type["nodes"] if node["id"] == "open_orders")["predicate"] = {
        "e": "bin", "op": "=",
        "left": {"e": "col", "name": "region"},
        "right": {"e": "lit", "value": 1, "type": "INT"},
    }
    show_rejection("TEXT column compared with INT", wrong_type, catalog)

    bad_field = json.loads(json.dumps(valid))
    next(node for node in bad_field["nodes"] if node["id"] == "open_orders")["predicate"] = {
        "e": "bin", "op": "=",
        "left": {"e": "col", "name": "does_not_exist"},
        "right": {"e": "lit", "value": 1, "type": "INT"},
    }
    show_rejection("unknown column binding", bad_field, catalog)

    bad_version = json.loads(json.dumps(valid))
    bad_version["version"] = 2
    show_rejection("unsupported document version", bad_version, catalog)


if __name__ == "__main__":
    main()
