"""本地合成关系表：不调用任何 SQL 引擎，数据直接以元组列表提供。

数据刻意包含：
- NULL 值（age / city / amount / user_id），用于验证三值逻辑与聚合忽略 NULL；
- 完全重复的行（多重集合语义）；
- 无法匹配的外键（user_id=9、user_id=NULL），用于验证内连接语义。
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Table:
    name: str
    columns: tuple
    rows: tuple


users = Table(
    "users",
    ("user_id", "name", "age", "city"),
    (
        (1, "alice", 34, "shanghai"),
        (2, "bob", 28, "beijing"),
        (3, "carol", None, "shanghai"),   # age 为 NULL
        (4, "dave", 45, None),            # city 为 NULL
        (5, "erin", 28, "beijing"),
        (6, "frank", 52, "shenzhen"),
        (7, "grace", None, None),         # 两个 NULL
        (5, "erin", 28, "beijing"),       # 完全重复行（bag 语义）
    ),
)

orders = Table(
    "orders",
    ("order_id", "user_id", "amount", "status"),
    (
        (101, 1, 199.0, "paid"),
        (102, 1, 25.5, "paid"),
        (103, 2, None, "pending"),        # amount 为 NULL
        (104, 3, 80.0, "paid"),
        (105, 3, 80.0, "paid"),
        (106, None, 60.0, "paid"),        # user_id 为 NULL，连接永不匹配
        (107, 9, 70.0, "shipped"),        # 无对应用户
        (108, 5, 15.0, "paid"),
        (109, 5, 15.0, "paid"),           # 完全重复行（bag 语义）
        (110, 6, None, "cancelled"),      # amount 为 NULL
        (111, 2, 44.0, "paid"),
        (112, 4, 300.0, "shipped"),
    ),
)


def make_bulk_table(n: int, name: str = "bulk") -> Table:
    """确定性地生成 n 行大表，用于排序落盘 / 预算演示。"""
    rows = tuple(
        (i, f"k{(i * 37) % 50:02d}", (i * 91) % 1000)
        for i in range(n)
    )
    return Table(name, ("id", "tag", "v"), rows)
