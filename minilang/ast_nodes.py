"""AST node definitions.

Every node stores ``start``/``end`` as a UTF-8 byte range into the source,
inherited from the tokens that make up the construct.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Optional


@dataclass
class Node:
    start: int  # UTF-8 byte offset, inclusive
    end: int  # UTF-8 byte offset, exclusive


# -- statements ---------------------------------------------------------


@dataclass
class Program(Node):
    stmts: list


@dataclass
class LetStmt(Node):
    name: str
    init: Optional[Node]


@dataclass
class ExprStmt(Node):
    expr: Node


@dataclass
class Block(Node):
    stmts: list


@dataclass
class IfStmt(Node):
    cond: Node
    then_branch: Node
    else_branch: Optional[Node]


@dataclass
class WhileStmt(Node):
    cond: Node
    body: Node


@dataclass
class ForStmt(Node):
    init: Optional[Node]
    cond: Optional[Node]
    step: Optional[Node]
    body: Node


@dataclass
class BreakStmt(Node):
    pass


@dataclass
class ContinueStmt(Node):
    pass


@dataclass
class ReturnStmt(Node):
    value: Optional[Node]


@dataclass
class ThrowStmt(Node):
    value: Node


@dataclass
class TryStmt(Node):
    body: Block
    catch_param: Optional[str]
    catch_body: Optional[Block]
    finally_body: Optional[Block]


@dataclass
class FuncDecl(Node):
    name: str
    params: list
    body: Block


# -- expressions --------------------------------------------------------


@dataclass
class Literal(Node):
    value: Any


@dataclass
class Var(Node):
    name: str


@dataclass
class Assign(Node):
    name: str
    value: Node


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
    op: str  # "AND" or "OR"
    left: Node
    right: Node


@dataclass
class Call(Node):
    callee: Node
    args: list = field(default_factory=list)
