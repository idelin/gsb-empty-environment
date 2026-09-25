"""AST node definitions. Every node carries a UTF-8 byte span (start, end)."""

from __future__ import annotations

from dataclasses import dataclass, field

Span = tuple[int, int]


# ---- expressions ----
@dataclass
class IntLit:
    value: int
    span: Span


@dataclass
class Var:
    name: str
    span: Span


@dataclass
class Input:
    index: int
    span: Span


@dataclass
class BinOp:
    op: str
    left: "Expr"
    right: "Expr"
    span: Span


@dataclass
class Neg:
    operand: "Expr"
    span: Span


Expr = IntLit | Var | Input | BinOp | Neg


# ---- statements ----
@dataclass
class Let:
    name: str
    expr: Expr
    span: Span


@dataclass
class Assign:
    name: str
    expr: Expr
    span: Span


@dataclass
class If:
    cond: Expr
    then_body: list["Stmt"]
    else_body: list["Stmt"]
    span: Span


@dataclass
class While:
    cond: Expr
    body: list["Stmt"]
    span: Span


@dataclass
class Output:
    expr: Expr
    span: Span


Stmt = Let | Assign | If | While | Output


@dataclass
class Program:
    body: list[Stmt]
    span: Span
