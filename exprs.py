"""表达式抽象语法树。

每个表达式显式暴露 refs()（被引用的限定列名集合），
列裁剪据此保留表达式的隐式依赖；eval() 在局部行字典上求值，
NULL 用 None 表示，遵循 SQL 三值逻辑（UNKNOWN 以 None 表示）。
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any


class Expr:
    def refs(self) -> frozenset[str]:
        raise NotImplementedError

    def eval(self, row: dict[str, Any]) -> Any:
        raise NotImplementedError


@dataclass(frozen=True)
class Col(Expr):
    """限定列引用，形如 'users.age'；同名列按来源表区分。"""

    name: str

    def refs(self) -> frozenset[str]:
        return frozenset({self.name})

    def eval(self, row: dict[str, Any]) -> Any:
        return row[self.name]


@dataclass(frozen=True)
class Lit(Expr):
    value: Any

    def refs(self) -> frozenset[str]:
        return frozenset()

    def eval(self, row: dict[str, Any]) -> Any:
        return self.value


def sql_and(a: Any, b: Any) -> Any:
    if a is False or b is False:
        return False
    if a is None or b is None:
        return None
    return True


def sql_or(a: Any, b: Any) -> Any:
    if a is True or b is True:
        return True
    if a is None or b is None:
        return None
    return False


@dataclass(frozen=True)
class BinOp(Expr):
    """二元运算。比较/算术遇 NULL 传播为 UNKNOWN(None)；and/or 走三值逻辑。"""

    op: str
    left: Expr
    right: Expr

    def refs(self) -> frozenset[str]:
        return self.left.refs() | self.right.refs()

    def eval(self, row: dict[str, Any]) -> Any:
        if self.op == "and":
            return sql_and(self.left.eval(row), self.right.eval(row))
        if self.op == "or":
            return sql_or(self.left.eval(row), self.right.eval(row))
        left = self.left.eval(row)
        right = self.right.eval(row)
        if left is None or right is None:
            return None
        if self.op == "=":
            return left == right
        if self.op == "!=":
            return left != right
        if self.op == "<":
            return left < right
        if self.op == "<=":
            return left <= right
        if self.op == ">":
            return left > right
        if self.op == ">=":
            return left >= right
        if self.op == "+":
            return left + right
        if self.op == "-":
            return left - right
        if self.op == "*":
            return left * right
        raise ValueError(f"未知运算符: {self.op}")


@dataclass(frozen=True)
class Not(Expr):
    operand: Expr

    def refs(self) -> frozenset[str]:
        return self.operand.refs()

    def eval(self, row: dict[str, Any]) -> Any:
        value = self.operand.eval(row)
        return None if value is None else (not value)


@dataclass(frozen=True)
class IsNull(Expr):
    operand: Expr

    def refs(self) -> frozenset[str]:
        return self.operand.refs()

    def eval(self, row: dict[str, Any]) -> Any:
        return self.operand.eval(row) is None
