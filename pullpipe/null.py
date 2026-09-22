"""SQL 风格的 NULL 与三值逻辑（3VL）。

* NULL 用单例 ``Null`` 表示；任何含 NULL 的比较 / 算术结果仍是 NULL。
* 布尔存在三态：True / False / Null（Unknown）。``is_true`` 是 SQL WHERE
  的判定规则：只有 True 通过，False 和 Unknown 都不通过。
* ``Null`` 不允许在 Python 布尔上下文里隐式求值，避免把 Unknown 误当 False。
* ``Null == Null`` 为 Null（SQL 中 NULL 不等于 NULL）；但 Python 集合/字典
  语义下 ``hash(Null)`` 恒定且 ``is`` 同一单例，因此 GROUP BY 可把所有 NULL
  聚到同一组——这是 SQL 标准对分组的规定，而不是等值比较。
"""

from __future__ import annotations

from typing import Any


class _Null:
    _instance: "_Null | None" = None

    def __new__(cls) -> "_Null":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "NULL"

    def __str__(self) -> str:
        return "NULL"

    def __bool__(self) -> bool:
        raise TypeError("NULL has no boolean truth value; use is_true/is_false/and3/or3/not3")

    def __hash__(self) -> int:
        return 0x5E5E5E

    def __eq__(self, other: object) -> "_Null":
        return self

    def __ne__(self, other: object) -> "_Null":
        return self

    def __lt__(self, other: object) -> "_Null":
        return self

    def __le__(self, other: object) -> "_Null":
        return self

    def __gt__(self, other: object) -> "_Null":
        return self

    def __ge__(self, other: object) -> "_Null":
        return self

    def __add__(self, other: object) -> "_Null":
        return self

    __radd__ = __add__
    __sub__ = __add__
    __rsub__ = __add__
    __mul__ = __add__
    __rmul__ = __add__
    __truediv__ = __add__
    __rtruediv__ = __add__
    __floordiv__ = __add__
    __mod__ = __add__
    __neg__ = __add__
    __abs__ = __add__


Null = _Null()
Tristate = bool | _Null


def is_null(value: Any) -> bool:
    return value is Null


def not3(value: Any) -> Tristate:
    """SQL NOT: NOT NULL = NULL(Unknown)。"""
    if value is Null:
        return Null
    return not bool(value)


def and3(left: Any, right: Any) -> Tristate:
    """SQL AND（短路）：遇 False 立即为 False；NULL 与 True 得 Unknown。"""
    if left is False or right is False:
        return False
    if left is Null or right is Null:
        return Null
    return True


def or3(left: Any, right: Any) -> Tristate:
    """SQL OR（短路）：遇 True 立即为 True；NULL 与 False 得 Unknown。"""
    if left is True or right is True:
        return True
    if left is Null or right is Null:
        return Null
    return False


def is_true(value: Any) -> bool:
    """WHERE/HAVING 判定：仅 True 通过。"""
    return value is True


def is_false(value: Any) -> bool:
    return value is False


def _both_numbers(left: Any, right: Any) -> bool:
    return left is not Null and right is not Null and isinstance(left, (int, float)) and isinstance(
        right, (int, float)
    )


def cmp3(left: Any, op: str, right: Any) -> Tristate:
    """标量比较，任一为 NULL 则 Unknown。"""
    if left is Null or right is Null:
        return Null
    if op == "=":
        return left == right
    if op == "<>":
        return left != right
    if op == "<":
        return left < right
    if op == "<=":
        return left <= right
    if op == ">":
        return left > right
    if op == ">=":
        return left >= right
    raise ValueError(f"unknown comparison operator: {op}")


def is_distinct(left: Any, right: Any) -> bool:
    """SQL ``IS DISTINCT FROM``：NULL 被当作已知值，结果永不为 Unknown。"""
    if left is Null:
        return right is not Null
    if right is Null:
        return True
    return left != right


def arith3(left: Any, op: str, right: Any) -> Any:
    """算术运算；任一为 NULL 则 NULL；除零抛 ZeroDivisionError（沿用 SQL 报错语义）。"""
    if left is Null or right is Null:
        return Null
    if not _both_numbers(left, right):
        raise TypeError(f"arithmetic on non-numeric values: {left!r} {op} {right!r}")
    if op == "+":
        return left + right
    if op == "-":
        return left - right
    if op == "*":
        return left * right
    if op == "/":
        return left / right
    raise ValueError(f"unknown arithmetic operator: {op}")
