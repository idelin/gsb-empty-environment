"""运行样例：python3 demo.py

场景 1：orders 表按 amount 整数范围分区，演示边界、OR 并集、NULL 分区、未绑定参数。
场景 2：users 表按 level 有限枚举分区，演示枚举裁剪与 catch-all。
"""

from minisql import (And, Compare, EnumScheme, Interval, IsNull, Not, Or,
                     Param, PartitionedTable, RangeScheme, analyze, select,
                     select_full_scan)


def show(title, table, pred, params=None, analyze_params="same"):
    print(f"\n=== {title} ===")
    print(f"谓词: {pred}" + (f"    参数: {params}" if params else ""))
    ap = params if analyze_params == "same" else analyze_params
    plan = analyze(pred, table, ap)
    for d in plan:
        mark = "扫描" if d.kept else "跳过"
        print(f"  [{mark}] {d.partition:<8} {d.proof}")
    res = select(table, pred, params, decisions=plan)
    full = select_full_scan(table, pred, params)
    ok = "✓ 与全表扫描一致" if res.rows == full else "✗ 不一致！"
    print(f"  结果 {len(res.rows)} 行: {[(r['id'], r.get(table.key_column)) for r in res.rows]}  {ok}")


def main():
    scheme = RangeScheme([
        ("p_low", Interval.of(None, True, 0, False)),     # (-inf, 0)
        ("p_mid", Interval.of(0, True, 100, False)),      # [0, 100)
        ("p_high", Interval.of(100, True, None, True)),   # [100, +inf)
    ])
    orders = PartitionedTable("orders", ["id", "amount", "tag"], "amount", scheme)
    for row in [
        {"id": 1, "amount": -50, "tag": "x"},
        {"id": 2, "amount": 0, "tag": "x"},      # 边界：p_mid 闭下界
        {"id": 3, "amount": 99, "tag": "y"},
        {"id": 4, "amount": 100, "tag": "y"},    # 边界：p_high 闭下界
        {"id": 5, "amount": 100, "tag": "y"},    # 重复行（多重集合）
        {"id": 6, "amount": 800, "tag": "z"},
        {"id": 7, "amount": None, "tag": "n"},   # NULL -> 独立分区
    ]:
        orders.insert(row)

    print("表 orders 的分区分布:")
    for p in orders.partitions:
        print(f"  {p.name:<8} 约束={p.constraint}  行数={len(p.rows)}")

    show("Q1 点查（边界值 100）", orders, Compare("amount", "=", 100))
    show("Q2 半开区间 [0, 100)", orders,
         And((Compare("amount", ">=", 0), Compare("amount", "<", 100))))
    show("Q3 OR 跨分区并集", orders,
         Or((Compare("amount", "<", 0), Compare("amount", ">=", 100))))
    show("Q4 NOT 下推", orders, Not(Compare("amount", "<", 100)))
    show("Q5 NULL 分区独立处理", orders, IsNull("amount"))
    show("Q6 参数未绑定（分析阶段保守保留）", orders,
         Compare("amount", "=", Param("1")), params={"1": 100}, analyze_params=None)
    show("Q7 同一预编译语句绑定参数后（精确裁剪）", orders,
         Compare("amount", "=", Param("1")), params={"1": 100})

    enum_scheme = EnumScheme(
        {"p_std": {"bronze", "silver"}, "p_prem": {"gold"}},
        catch_all="p_other",
    )
    users = PartitionedTable("users", ["id", "level"], "level", enum_scheme)
    for row in [
        {"id": 1, "level": "bronze"},
        {"id": 2, "level": "gold"},
        {"id": 3, "level": "diamond"},   # 未列出 -> catch-all
        {"id": 4, "level": None},        # NULL -> 独立分区
    ]:
        users.insert(row)

    print("\n表 users 的分区分布:")
    for p in users.partitions:
        print(f"  {p.name:<8} 约束={p.constraint}  行数={len(p.rows)}")

    show("Q8 枚举点查", users, Compare("level", "=", "gold"))
    show("Q9 枚举 OR 并集（含 catch-all）", users,
         Or((Compare("level", "=", "bronze"), Compare("level", "=", "diamond"))))


if __name__ == "__main__":
    main()
