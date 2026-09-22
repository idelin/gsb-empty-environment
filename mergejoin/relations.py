"""本地合成的关系表（有序流数据源）。

不连接任何数据库：小表是普通列表，大表用惰性生成器逐行产出，
模拟"可溢写的有序输入流"——即使键组远大于内存，上游也不会整体驻留。

列定义：``(key, value)``，键列在前。
"""

from __future__ import annotations

from collections.abc import Iterator

# 小关系表：包含 NULL 键、2×3 重复组、单侧键等情形。
LEFT_ROWS: list[tuple] = [
    (None, "L_null"),
    (1, "L_1"),
    (2, "L_2a"),
    (2, "L_2b"),
    (4, "L_4"),
    (5, "L_5"),
]

RIGHT_ROWS: list[tuple] = [
    (None, "R_null"),
    (1, "R_1"),
    (2, "R_2a"),
    (2, "R_2b"),
    (2, "R_2c"),
    (3, "R_3"),
    (4, "R_4"),
]

BIG_GROUP_SIZE = 1000  # key=2 组的行数，两侧各这么多行。


def big_left_rows() -> Iterator[tuple]:
    """有序左侧流：key=2 是一个 1000 行的大组，惰性产出。"""
    yield (1, "L_1")
    for i in range(BIG_GROUP_SIZE):
        yield (2, f"L_2_{i}")
    yield (4, "L_4")


def big_right_rows() -> Iterator[tuple]:
    """有序右侧流：key=2 同样是 1000 行大组，另含单侧键 3。"""
    yield (1, "R_1")
    for i in range(BIG_GROUP_SIZE):
        yield (2, f"R_2_{i}")
    yield (3, "R_3")
    yield (4, "R_4")


def one_sided_big_left() -> Iterator[tuple]:
    """有序左侧流：key=7 大组，右侧只有一行——仅左组溢写。"""
    for i in range(BIG_GROUP_SIZE):
        yield (7, f"L_7_{i}")
