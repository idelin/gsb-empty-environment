"""运行样例：反向列裁剪 + 本地执行，打印裁剪证据与结果。"""
from __future__ import annotations

from data import USERS, USERS_SCHEMA, ORDERS, ORDERS_SCHEMA
from exprs import BinOp, Col, Lit
from plan import Agg, Aggregate, Evidence, Filter, Join, Project, Scan, Sort, prune


def fmt(value: object) -> str:
    return "NULL" if value is None else str(value)


def show(title: str, sql: str, needed: frozenset[str], plan_root) -> None:
    print("=" * 72)
    print(f"【{title}】")
    print(sql)
    print("-" * 72)
    pruned, evidence = prune(plan_root, needed)
    print("裁剪证据（自底向上）:")
    for ev in evidence:
        print(f"  {ev.operator}")
        print(f"    保留: {list(ev.kept) or '（零列 —— 行数仍保留）'}")
        print(f"    裁掉: {list(ev.pruned) or '（无）'}")
        print(f"    原因: {ev.reason}")
    cols, rows = pruned.run()
    print("-" * 72)
    print(f"结果（{len(rows)} 行）: {cols}")
    for row in rows:
        print("  " + " | ".join(fmt(row[c]) for c in cols))
    print()


def main() -> None:
    users = Scan("users", USERS_SCHEMA, USERS)
    orders = Scan("orders", ORDERS_SCHEMA, ORDERS)

    # 查询 1：输出不含排序列 city，但执行须保留到排序后；email/note/id 被裁剪
    q1 = Project(
        (("name", Col("users.name")), ("amount", Col("orders.amount"))),
        Sort(
            ((Col("users.city"), True),),  # ORDER BY users.city DESC
            Filter(
                BinOp("and",
                      BinOp("=", Col("orders.status"), Lit("paid")),
                      BinOp(">=", Col("users.age"), Lit(18))),
                Join(
                    BinOp("=", Col("users.id"), Col("orders.user_id")),
                    users, orders,
                ),
            ),
        ),
    )
    show(
        "查询 1：连接 + 过滤 + 排序（排序键不在输出中）",
        "SELECT u.name, o.amount FROM users u JOIN orders o ON u.id = o.user_id\n"
        "WHERE o.status = 'paid' AND u.age >= 18 ORDER BY u.city DESC;",
        frozenset({"name", "amount"}),
        q1,
    )

    # 查询 2：COUNT(*) —— 扫描裁至零列，行数不变，结果不是空表
    q2 = Aggregate((), (Agg("count_star", None, "cnt"),), Scan("users", USERS_SCHEMA, USERS))
    show(
        "查询 2：COUNT(*) 全表计数（零列裁剪）",
        "SELECT COUNT(*) AS cnt FROM users;",
        frozenset({"cnt"}),
        q2,
    )

    # 查询 3：GROUP BY 含 NULL 键；COUNT(col) 忽略 NULL
    q3 = Aggregate(
        (("city", Col("users.city")),),
        (Agg("count_star", None, "rows"), Agg("count", Col("users.age"), "aged")),
        Scan("users", USERS_SCHEMA, USERS),
    )
    show(
        "查询 3：分组聚合（NULL 成组、COUNT(age) 忽略 NULL）",
        "SELECT city, COUNT(*) AS rows, COUNT(age) AS aged FROM users GROUP BY city;",
        frozenset({"city", "rows", "aged"}),
        q3,
    )


if __name__ == "__main__":
    main()
