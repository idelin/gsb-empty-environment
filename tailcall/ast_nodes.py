"""抽象语法树节点。

每个节点的 span 都是源码的 UTF-8 字节区间 [start, end)。
"""

from dataclasses import dataclass


@dataclass
class Node:
    span: tuple


@dataclass
class IntLit(Node):
    value: int


@dataclass
class BoolLit(Node):
    value: bool


@dataclass
class StrLit(Node):
    value: str


@dataclass
class Var(Node):
    name: str


@dataclass
class BinOp(Node):
    op: str
    left: Node
    right: Node


@dataclass
class UnaryOp(Node):
    op: str
    operand: Node


@dataclass
class If(Node):
    cond: Node
    then: Node
    otherwise: Node


@dataclass
class Let(Node):
    name: str
    value: Node
    body: Node


@dataclass
class Call(Node):
    callee: Node
    args: list


@dataclass
class TryFinally(Node):
    body: Node
    finalizer: Node


@dataclass
class FunctionDef:
    name: str
    params: list
    body: Node
    span: tuple


@dataclass
class Program:
    defs: list
    main: Node
