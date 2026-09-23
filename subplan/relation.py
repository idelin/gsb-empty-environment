"""关系表与 NULL / 多重集合（bag）语义约定。

约定（不依赖任何 SQL 引擎，纯标准库实现）：

- 一行是一个 tuple；表是行的**多重集合**：重复行保留，不自动去重，
  Filter/Project 不改变基数，Join 按 bag 语义做乘法展开。
- NULL 用 ``None`` 表示。
- 比较遵循三值逻辑：任何与 NULL 的比较结果为 UNKNOWN（这里用 ``None``
  表示）；``Filter`` 只保留谓词结果 ``is True`` 的行，UNKNOWN 与 False
  一样被丢弃。
- 等值连接中 NULL 不等于任何值，包括 NULL 本身。
"""


def sql_eq(left, right):
    """三值逻辑等值比较：任一侧为 NULL 时返回 None（UNKNOWN）。"""
    if left is None or right is None:
        return None
    return left == right


def sql_ne(left, right):
    if left is None or right is None:
        return None
    return left != right


def sql_lt(left, right):
    if left is None or right is None:
        return None
    return left < right


def sql_le(left, right):
    if left is None or right is None:
        return None
    return left <= right


def sql_gt(left, right):
    if left is None or right is None:
        return None
    return left > right


def sql_ge(left, right):
    if left is None or right is None:
        return None
    return left >= right


class Table:
    """内存中的合成关系表（多重集合）。"""

    def __init__(self, name, columns, rows):
        self.name = name
        self.columns = tuple(columns)
        self.rows = [tuple(row) for row in rows]

    def col_index(self, column):
        return self.columns.index(column)

    def __repr__(self):
        return f"Table({self.name!r}, cols={len(self.columns)}, rows={len(self.rows)})"
