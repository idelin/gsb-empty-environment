"""运行样例：python3 main.py

演示：
1. 完整查询（连接 + 过滤 + 分组聚合），正常结束。
2. 同一查询加步骤预算 -> 部分聚合，带未完成标记（不冒充最终值）。
3. LIMIT 正常提前结束，与预算中断区分。
4. 排序落盘：临时文件创建与释放。
5. 取消：临时资源释放。
6. 嵌套循环重复子计划共享预算，记账不遗漏。
"""
from releng import (
    Budget,
    Filter,
    HashAggregate,
    HashJoin,
    Limit,
    NestedLoopJoin,
    ResourceRegistry,
    Scan,
    Sort,
    Table,
    execute,
    make_bulk_table,
    orders,
    sql_eq,
    sql_gt,
    users,
)


def show(title, result):
    print(f"\n== {title} ==")
    print(f"complete={result.complete} termination={result.termination.name} "
          f"partial_aggregates={result.partial_aggregates}")
    s = result.stats
    print(f"stats: rows={s.rows_processed} steps={s.steps} "
          f"peak_mem={s.peak_memory}B temp_files={s.temp_files_created}")
    for row in result.rows[:8]:
        print(f"  {row}")
    if len(result.rows) > 8:
        print(f"  ... 共 {len(result.rows)} 行")


def city_paid_plan():
    """每个城市已支付订单的总额与订单数。"""
    paid = Filter(Scan(orders), lambda r: sql_eq(r["status"], "paid"))
    joined = HashJoin(paid, Scan(users), "user_id", "user_id")
    return HashAggregate(
        joined,
        group_by=["city"],
        aggregates=[
            ("count_star", None, "n_orders"),
            ("sum", "amount", "total"),
            ("count", "amount", "n_amount"),
        ],
    )


def main():
    print("== 合成表 ==")
    print(f"users: {len(users.rows)} 行（含 NULL 与重复行）")
    print(f"orders: {len(orders.rows)} 行（含 NULL 与重复行）")

    # 1. 完整聚合查询
    show("1. 完整聚合：各城市已支付订单", execute(city_paid_plan()))

    # 2. 步骤预算中断 -> 部分聚合
    show("2. 步骤预算=50 -> 部分聚合（非最终值）",
         execute(city_paid_plan(), Budget(max_steps=50)))

    # 3. LIMIT 正常提前结束
    show("3. LIMIT 3（正常提前结束）",
         execute(Limit(Scan(users), 3)))
    show("3b. 行数预算=3（预算中断，对照）",
         execute(Scan(users), Budget(max_rows=3)))

    # 4. 排序落盘与临时文件释放
    registry = ResourceRegistry()
    result = execute(Sort(Scan(make_bulk_table(300)), "v", spill_bytes=4096),
                     registry=registry)
    show("4. 排序落盘（spill_bytes=4096）", result)
    print(f"排序后剩余临时文件: {registry.active_temp_files()}")

    # 5. 取消后临时资源释放
    registry = ResourceRegistry()
    result = execute(Sort(Scan(make_bulk_table(300)), "v", spill_bytes=4096),
                     budget=Budget(cancel_after_steps=120),
                     registry=registry)
    show("5. 执行到第 120 步被取消", result)
    print(f"取消后剩余临时文件: {registry.active_temp_files()}")

    # 6. 嵌套循环：内层子计划重复执行，共享预算记账
    a = Table("a", ("x",), ((1,), (2,), (3,)))
    b = Table("b", ("y",), ((10,), (20,), (30,), (40,)))
    plan = NestedLoopJoin(Scan(a), Scan(b), lambda l, r: sql_gt(l["x"], 0))
    show("6. 嵌套循环连接 3x4（内层重复执行 3 次）", execute(plan))


if __name__ == "__main__":
    main()
