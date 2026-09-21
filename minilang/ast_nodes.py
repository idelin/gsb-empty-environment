"""AST node definitions. Every node carries a UTF-8 byte span."""
from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class Expr:
    start: int
    end: int


@dataclass
class IntLit(Expr):
    value: int


@dataclass
class Var(Expr):
    name: str


@dataclass
class Unary(Expr):
    op: str
    operand: Expr


@dataclass
class BinOp(Expr):
    op: str
    left: Expr
    right: Expr


@dataclass
class Stmt:
    start: int
    end: int


@dataclass
class Let(Stmt):
    name: str
    value: Expr


@dataclass
class Assign(Stmt):
    name: str
    value: Expr


@dataclass
class Print(Stmt):
    value: Expr


@dataclass
class If(Stmt):
    cond: Expr
    then_body: list[Stmt] = field(default_factory=list)
    else_body: list[Stmt] = field(default_factory=list)


@dataclass
class While(Stmt):
    cond: Expr
    body: list[Stmt] = field(default_factory=list)


@dataclass
class Halt(Stmt):
    pass
