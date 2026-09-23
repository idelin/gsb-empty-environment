from partition_pruning import (
    Cmp,
    Col,
    ColumnSpec,
    EnumFragment,
    Lit,
    Not,
    NullFragment,
    Param,
    PartitionScheme,
    RangeFragment,
    RelationTable,
    and_,
    in_,
    or_,
    predicate_text,
    prune,
    render_skip_proof,
    select_rows,
)


def build_orders():
    scheme = PartitionScheme.build(
        [ColumnSpec("amount", "integer"), ColumnSpec("region", "enum")],
        {
            "amount": (
                RangeFragment("neg", None, -1),
                RangeFragment("zero_to_nine", 0, 9),
                RangeFragment("ten_to_ninety_nine", 10, 99),
                RangeFragment("large", 100, None),
                NullFragment("null_amount"),
            ),
            "region": (
                EnumFragment("east", frozenset({"east"})),
                EnumFragment("west", frozenset({"west"})),
                EnumFragment("central", frozenset({"central"})),
                NullFragment("null_region"),
            ),
        },
    )
    table = RelationTable("orders", scheme)
    rows = [
        (1, 0, "east", "A"),
        (2, 9, "east", "B"),
        (3, 10, "west", "C"),
        (4, 99, "west", "D"),
        (5, 100, "central", "E"),
        (6, None, "east", "F"),
        (7, 50, None, "G"),
        (8, 5, "central", "H"),
    ]
    for order_id, amount, region, label in rows:
        table.insert(
            {"id": order_id, "amount": amount, "region": region, "label": label}
        )
    return table


def show(title, predicate, table, params=None):
    print(f"\n{title}")
    print(f"谓词: {predicate_text(predicate)}")
    plan = prune(predicate, table.scheme, params or {})
    print("选中:", ", ".join(partition.id for partition in plan.selected))
    print("跳过:", len(plan.skipped), "个分区")
    for skip in plan.skipped[:3]:
        print(render_skip_proof(skip, "  "))
    if len(plan.skipped) > 3:
        print(f"  …另有 {len(plan.skipped) - 3} 个跳过证明具有相同结构")
    rows = select_rows(table, predicate, params, ("id", "amount", "region", "label"))
    print("命中行:", rows)


def main():
    table = build_orders()

    boundary = Cmp("=", Col("amount"), Lit(10))
    show("1) 整数闭区间边界：amount = 10", boundary, table)

    branches = or_(
        Cmp("<=", Col("amount"), Lit(9)),
        Cmp("=", Col("region"), Lit("west")),
    )
    show("2) OR 跨分区取并集", branches, table)

    predicate = and_(
        in_(Col("region"), (Lit("east"), Lit("central"))),
        Not(Cmp("=", Col("amount"), Lit(9))),
    )
    show("3) 枚举 IN 与 NOT", predicate, table)

    nulls = and_(
        Cmp(">=", Col("amount"), Param("min_amount")),
        Cmp("=", Col("region"), Param("target_region")),
    )
    show("4) 参数未绑定：保守保留全部叶子分区", nulls, table, {})
    show("4a) 参数绑定后：重新裁剪", nulls, table, {"min_amount": 50, "target_region": "west"})


if __name__ == "__main__":
    main()
