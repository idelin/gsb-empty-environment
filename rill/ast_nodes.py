"""Abstract syntax tree for Rill."""

from __future__ import annotations

from dataclasses import dataclass, field

from .types import Type

Span = tuple[int, int]


@dataclass
class Param:
    name: str
    type: Type
    mode: str  # "readonly" | "writable"


@dataclass
class Function:
    name: str
    params: list[Param]
    return_type: Type
    body: "Block"
    span: Span


# --- expressions -----------------------------------------------------------

@dataclass
class Expr:
    span: Span


@dataclass
class IntLit(Expr):
    value: int


@dataclass
class StrLit(Expr):
    value: str


@dataclass
class BoolLit(Expr):
    value: bool


@dataclass
class RecordLit(Expr):
    fields: list[tuple[str, "Expr"]]


@dataclass
class Var(Expr):
    name: str


@dataclass
class Field(Expr):
    target: Expr
    name: str
    name_span: Span = (0, 0)


@dataclass
class Call(Expr):
    callee: str
    args: list[Expr]


@dataclass
class Unary(Expr):
    op: str
    operand: Expr


@dataclass
class Binary(Expr):
    op: str
    left: Expr
    right: Expr


# --- statements ------------------------------------------------------------

@dataclass
class Stmt:
    span: Span


@dataclass
class ExprStmt(Stmt):
    expr: Expr


@dataclass
class Let(Stmt):
    name: str
    declared: Type | None
    value: Expr
    mutable: bool


@dataclass
class Assign(Stmt):
    target: Expr  # Var or Field only
    value: Expr


@dataclass
class Extend(Stmt):
    target: Expr
    field: str
    value: Expr


@dataclass
class If(Stmt):
    cond: Expr
    then_block: "Block"
    else_block: "Block | None"


@dataclass
class While(Stmt):
    cond: Expr
    body: "Block"


@dataclass
class Return(Stmt):
    value: Expr | None


@dataclass
class Block(Stmt):
    stmts: list[Stmt]


@dataclass
class Program:
    functions: dict[str, Function] = field(default_factory=dict)
    order: list[str] = field(default_factory=list)
    main: Block = field(default_factory=lambda: Block((0, 0), []))
