"""标量值与类型处理。

支持四种列类型：int64 / float64 / bool / string（按名字对齐 Arrow 风格）。
规则：
- ``None`` 表示 SQL 风格的 null；
- ``float('nan')`` 仅在 float64 列中合法，且与 null 不同，NaN != NaN；
- int64 只接受真正的 int（bool 不算 int），整数全程精确计算，
  不会把 int 端点提升为 float；
- 校验是“精确类型”校验：float 列不接受 int，反之亦然。
"""
from __future__ import annotations

import math
from typing import Any

INT64 = "int64"
FLOAT64 = "float64"
BOOL = "bool"
STRING = "string"

TYPES = (INT64, FLOAT64, BOOL, STRING)


def is_null(v: Any) -> bool:
    """是否为 SQL 风格的 null（仅 ``None``）。NaN 不是 null。"""
    return v is None


def is_nan(v: Any) -> bool:
    """是否为 NaN。NaN 是浮点列里一个与 null 不同的特殊值。"""
    return isinstance(v, float) and math.isnan(v)


def is_missing(v: Any) -> bool:
    """null 或 NaN：两者都不参与连接，但语义不同。"""
    return v is None or (isinstance(v, float) and math.isnan(v))


def check_type(typ: str, v: Any) -> None:
    """按列类型校验单个标量；null/NaN 也必须出现在相容的列里。

    抛出 ``TypeError`` / ``ValueError``。
    """
    if v is None:
        return
    if typ == INT64:
        if isinstance(v, bool) or not isinstance(v, int):
            raise TypeError(f"int64 列只接受 int，收到 {v!r}")
    elif typ == FLOAT64:
        if not isinstance(v, float):
            raise TypeError(f"float64 列只接受 float，收到 {v!r}（int 不会被隐式提升）")
    elif typ == BOOL:
        if not isinstance(v, bool):
            raise TypeError(f"bool 列只接受 True/False，收到 {v!r}")
    elif typ == STRING:
        if not isinstance(v, str):
            raise TypeError(f"string 列只接受 str，收到 {v!r}")
    else:
        raise ValueError(f"未知列类型 {typ!r}，支持: {TYPES}")


def values_equal(a: Any, b: Any) -> bool:
    """null/NaN 安全的相等比较，且类型必须一致。

    - null 与任何值（含 null）都不相等（SQL 三值逻辑）；
    - NaN 与 NaN 不相等（IEEE 754），NaN 也不等于 null；
    - bool 与 int 即使在 Python 中 ``True == 1``，这里也判为不等。
    """
    if a is None or b is None:
        return False
    if isinstance(a, float) and math.isnan(a):
        return False
    if isinstance(b, float) and math.isnan(b):
        return False
    if isinstance(a, bool) != isinstance(b, bool):
        return False
    return a == b


def describe(v: Any) -> str:
    """用于输出/错误信息的可读表示，区分 null 与 NaN。"""
    if v is None:
        return "null"
    if isinstance(v, float) and math.isnan(v):
        return "NaN"
    return repr(v)
