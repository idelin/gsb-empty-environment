"""本地合成关系表（不写磁盘、不调 SQL）：大事实表、维度表、含 NULL 的小表。"""

from __future__ import annotations

from .null import Null
from .table import GeneratedTable, ListTable

ONE_MILLION = 1_000_000


def big_fact(rows: int = ONE_MILLION) -> GeneratedTable:
    """orders(id, cust_id, amount)：惰性生成百万行，构造时不产生任何行数据。"""

    def maker(n: int):
        # cust_id 循环 0..99；金额随 id 有规律，便于断言
        return (n, n % 100, (n % 7) * 10 + (n % 3))

    return GeneratedTable(("id", "cust_id", "amount"), rows, maker)


def customers() -> ListTable:
    """dim 小表：100 个客户，含一个 cust_id=99 的重复键（多重集合）和 2 个 NULL。"""
    rows = [(i, f"cust_{i:03d}") for i in range(100)]
    rows.append((99, "cust_099_dup"))   # 重复连接键 -> 连接结果翻倍
    rows.append((Null, "cust_null_1"))  # NULL 键，永不参与连接
    rows.append((Null, "cust_null_2"))
    return ListTable(("cust_id", "cname"), rows)


def events() -> ListTable:
    """含 NULL、重复行的多重集合，用于语义演示与测试。"""
    return ListTable(
        ("g", "v"),
        [
            ("a", 1),
            ("a", 3),
            ("a", Null),   # SUM/AVG/COUNT(v) 忽略；COUNT(*) 计入
            ("b", 10),
            ("b", 20),
            (Null, 7),     # NULL 分组键自成一组
            (Null, 8),
            ("a", 1),      # 重复值合法
        ],
    )
