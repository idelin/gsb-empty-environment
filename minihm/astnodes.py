"""AST nodes; every node carries its UTF-8 byte span."""
from __future__ import annotations

from dataclasses import dataclass

from .source import Span


@dataclass
class Node:
    span: Span


@dataclass
class IntLit(Node):
    value: int


@dataclass
class BoolLit(Node):
    value: bool


@dataclass
class Var(Node):
    name: str


@dataclass
class Lam(Node):
    param: str
    param_span: Span
    body: Node


@dataclass
class App(Node):
    fn: Node
    arg: Node


@dataclass
class Let(Node):
    name: str
    name_span: Span
    value: Node
    body: Node


@dataclass
class If(Node):
    cond: Node
    then: Node
    otherwise: Node


@dataclass
class BinOp(Node):
    op: str
    op_span: Span
    left: Node
    right: Node
