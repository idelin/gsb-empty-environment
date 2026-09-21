from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Token:
    kind: str
    text: str
    start: int  # UTF-8 byte offset, inclusive
    end: int    # UTF-8 byte offset, exclusive


@dataclass
class Node:
    start: int
    end: int


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
    param_start: int
    param_end: int
    body: Node


@dataclass
class App(Node):
    fn: Node
    arg: Node


@dataclass
class Let(Node):
    name: str
    name_start: int
    name_end: int
    rhs: Node
    body: Node


@dataclass
class BinOp(Node):
    op: str
    op_start: int
    op_end: int
    left: Node
    right: Node


@dataclass
class If(Node):
    cond: Node
    then: Node
    otherwise: Node
