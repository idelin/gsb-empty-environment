"""AST node definitions. `span` is a (start, end) UTF-8 byte range."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Optional

Span = tuple[int, int]


@dataclass
class Module:
    items: list[Any]
    span: Span


@dataclass
class Import:
    names: list[str]
    module: str
    span: Span


@dataclass
class ConstDecl:
    name: str
    expr: Any
    exported: bool
    span: Span


@dataclass
class FnDecl:
    name: str
    params: list[str]
    body: list[Any]
    exported: bool
    span: Span


@dataclass
class ReturnStmt:
    expr: Optional[Any]
    span: Span


@dataclass
class IfStmt:
    cond: Any
    then: list[Any]
    orelse: Optional[list[Any]]
    span: Span


@dataclass
class ExprStmt:
    expr: Any
    span: Span


@dataclass
class Num:
    value: Any
    span: Span


@dataclass
class Str:
    value: str
    span: Span


@dataclass
class Bool:
    value: bool
    span: Span


@dataclass
class Name:
    name: str
    span: Span


@dataclass
class Bin:
    op: str
    left: Any
    right: Any
    span: Span


@dataclass
class Unary:
    op: str
    operand: Any
    span: Span


@dataclass
class Call:
    func: Any
    args: list[Any]
    span: Span
