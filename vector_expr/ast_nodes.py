"""Untyped expression AST produced by the parser.

Every node records ``pos``, the character offset in the source string,
so compile errors can point at the offending token.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any


@dataclass
class Expr:
    pos: int


@dataclass
class Literal(Expr):
    value: Any  # int | float | bool | None


@dataclass
class ColumnRef(Expr):
    name: str


@dataclass
class UnaryOp(Expr):
    op: str  # '-' | 'NOT'
    operand: Expr


@dataclass
class BinaryOp(Expr):
    op: str  # + - * / % = != < <= > >= AND OR
    left: Expr
    right: Expr


@dataclass
class If(Expr):
    cond: Expr
    then: Expr
    otherwise: Expr


@dataclass
class Cast(Expr):
    operand: Expr
    target: str  # type name, resolved during type checking
