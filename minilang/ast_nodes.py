"""AST node definitions. Every node carries a UTF-8 byte span."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Optional


@dataclass
class Node:
    start: int  # UTF-8 byte offset, inclusive
    end: int    # UTF-8 byte offset, exclusive


# ---------- statements ----------

@dataclass
class Program(Node):
    body: list


@dataclass
class Block(Node):
    body: list


@dataclass
class Let(Node):
    name: str
    init: Node


@dataclass
class Assign(Node):
    name: str
    value: Node


@dataclass
class ExprStmt(Node):
    expr: Node


@dataclass
class If(Node):
    cond: Node
    then: Block
    otherwise: Optional[Node]  # Block | If | None


@dataclass
class While(Node):
    cond: Node
    body: Block


@dataclass
class FuncDef(Node):
    name: str
    params: list
    body: Block


@dataclass
class Return(Node):
    value: Optional[Node]


@dataclass
class Break(Node):
    pass


@dataclass
class Continue(Node):
    pass


@dataclass
class Throw(Node):
    value: Node


@dataclass
class Try(Node):
    body: Block
    catch_name: Optional[str]
    catch_body: Optional[Block]
    finally_body: Optional[Block]


# ---------- expressions ----------

@dataclass
class Literal(Node):
    value: Any


@dataclass
class Var(Node):
    name: str


@dataclass
class Binary(Node):
    op: str
    left: Node
    right: Node


@dataclass
class Unary(Node):
    op: str
    operand: Node


@dataclass
class Logical(Node):
    op: str  # "&&" or "||"
    left: Node
    right: Node


@dataclass
class Call(Node):
    callee: Node
    args: list
