"""AST nodes. Every node carries a UTF-8 byte span."""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Node:
    span: tuple[int, int]


@dataclass(frozen=True)
class IntLit(Node):
    value: int


@dataclass(frozen=True)
class Var(Node):
    name: str


@dataclass(frozen=True)
class BinOp(Node):
    op: str  # one of "+", "-", "*", "/"
    left: Node
    right: Node


@dataclass(frozen=True)
class Neg(Node):
    operand: Node


@dataclass(frozen=True)
class Binding:
    name: str
    name_span: tuple[int, int]
    expr: Node


@dataclass(frozen=True)
class Let(Node):
    """Recursive let: all bindings are in scope for each other and the body."""

    bindings: tuple[Binding, ...]
    body: Node
