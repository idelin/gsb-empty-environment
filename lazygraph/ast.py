"""AST 节点定义。每个节点都携带自己的字节区间。"""
from __future__ import annotations

from dataclasses import dataclass

from .sourcemap import Span


class Expr:
    span: Span


@dataclass
class IntLit(Expr):
    value: int
    span: Span


@dataclass
class BoolLit(Expr):
    value: bool
    span: Span


@dataclass
class Tick(Expr):
    """零参数内建：每次被强制计数 +1，返回当前累计值（从 1 开始）。"""

    span: Span


@dataclass
class Var(Expr):
    name: str
    span: Span


@dataclass
class Neg(Expr):
    operand: Expr
    span: Span


@dataclass
class BinOp(Expr):
    op: str
    left: Expr
    right: Expr
    span: Span


@dataclass
class If(Expr):
    cond: Expr
    then_expr: Expr
    else_expr: Expr
    span: Span


@dataclass
class Binding:
    name: str
    value: Expr
    name_span: Span
    span: Span  # 整个绑定的区间


@dataclass
class Let(Expr):
    rec: bool
    bindings: list[Binding]
    body: Expr
    span: Span


@dataclass
class Program:
    """顶层一系列 (非递归) let 绑定 + 最终表达式。"""

    bindings: list[Binding]
    result: Expr
    span: Span
