"""布尔谓词 AST：比较 / IS NULL / AND / OR / NOT，以及未绑定参数 Param。

行用 dict 表示，NULL 用 None 表示。求值遵循三值逻辑。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Mapping

from .logic import Tri

OPS = ("=", "!=", "<", "<=", ">", ">=")
FLIP = {"=": "!=", "!=": "=", "<": ">=", ">=": "<", ">": "<=", "<=": ">"}


@dataclass(frozen=True)
class Param:
    """查询参数，如 $1。分析阶段可能尚未绑定。"""

    name: str

    def __str__(self) -> str:
        return f"${self.name}"


class Expr:
    def eval(self, row: Mapping[str, Any], params: Mapping[str, Any] | None) -> Tri:
        raise NotImplementedError


def _resolve(value: Any, params: Mapping[str, Any] | None) -> Any:
    if isinstance(value, Param):
        if params is None or value.name not in params:
            raise ValueError(f"参数 {value} 未绑定，无法执行")
        return params[value.name]
    return value


def _cmp(op: str, left: Any, right: Any) -> bool:
    return {
        "=": left == right,
        "!=": left != right,
        "<": left < right,
        "<=": left <= right,
        ">": left > right,
        ">=": left >= right,
    }[op]


@dataclass(frozen=True)
class Compare(Expr):
    column: str
    op: str
    value: Any  # 字面量或 Param

    def __post_init__(self) -> None:
        if self.op not in OPS:
            raise ValueError(f"不支持的比较运算符: {self.op}")

    def eval(self, row: Mapping[str, Any], params: Mapping[str, Any] | None) -> Tri:
        left = row[self.column]
        right = _resolve(self.value, params)
        if left is None or right is None:
            return Tri.UNKNOWN  # NULL 参与任何比较都是 UNKNOWN
        return Tri.TRUE if _cmp(self.op, left, right) else Tri.FALSE

    def __str__(self) -> str:
        v = str(self.value) if isinstance(self.value, Param) else repr(self.value)
        return f"{self.column} {self.op} {v}"


@dataclass(frozen=True)
class IsNull(Expr):
    column: str
    negated: bool = False  # True 表示 IS NOT NULL

    def eval(self, row: Mapping[str, Any], params: Mapping[str, Any] | None) -> Tri:
        is_null = row[self.column] is None
        return Tri.TRUE if is_null != self.negated else Tri.FALSE

    def __str__(self) -> str:
        return f"{self.column} IS {'NOT ' if self.negated else ''}NULL"


@dataclass(frozen=True)
class And(Expr):
    terms: tuple[Expr, ...]

    def eval(self, row: Mapping[str, Any], params: Mapping[str, Any] | None) -> Tri:
        result = Tri.TRUE
        for term in self.terms:
            result = result & term.eval(row, params)
        return result

    def __str__(self) -> str:
        return "(" + " AND ".join(str(t) for t in self.terms) + ")"


@dataclass(frozen=True)
class Or(Expr):
    terms: tuple[Expr, ...]

    def eval(self, row: Mapping[str, Any], params: Mapping[str, Any] | None) -> Tri:
        result = Tri.FALSE
        for term in self.terms:
            result = result | term.eval(row, params)
        return result

    def __str__(self) -> str:
        return "(" + " OR ".join(str(t) for t in self.terms) + ")"


@dataclass(frozen=True)
class Not(Expr):
    child: Expr

    def eval(self, row: Mapping[str, Any], params: Mapping[str, Any] | None) -> Tri:
        return ~self.child.eval(row, params)

    def __str__(self) -> str:
        return f"NOT ({self.child})"


def negate(expr: Expr) -> Expr:
    """逻辑取反，并把 NOT 下推为叶子上的等价形式。"""
    if isinstance(expr, Compare):
        return Compare(expr.column, FLIP[expr.op], expr.value)
    if isinstance(expr, IsNull):
        return IsNull(expr.column, not expr.negated)
    if isinstance(expr, And):
        return Or(tuple(negate(t) for t in expr.terms))
    if isinstance(expr, Or):
        return And(tuple(negate(t) for t in expr.terms))
    if isinstance(expr, Not):
        return normalize(expr.child)
    raise TypeError(expr)


def normalize(expr: Expr) -> Expr:
    """消除 Not 节点：分析器只需处理 Compare / IsNull / And / Or。"""
    if isinstance(expr, Not):
        return negate(expr.child)
    if isinstance(expr, And):
        return And(tuple(normalize(t) for t in expr.terms))
    if isinstance(expr, Or):
        return Or(tuple(normalize(t) for t in expr.terms))
    return expr
