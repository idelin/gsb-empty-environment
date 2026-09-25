"""AST node definitions. Every span is a UTF-8 byte range [start, end)."""
from __future__ import annotations

from dataclasses import dataclass


@dataclass
class Node:
    span: tuple  # (start_byte, end_byte)


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
class Unary(Node):
    op: str        # 'neg' | 'not'
    operand: Node
    op_span: tuple


@dataclass
class Binary(Node):
    op: str        # '+','-','*','/','%','<','<=','>','>=','==','!=','and','or'
    left: Node
    right: Node
    op_span: tuple  # 运算符的字节区间，错误位置契约以它为准


@dataclass
class Let(Node):
    name: str
    name_span: tuple
    value: Node


@dataclass
class If(Node):
    cond: Node
    then_body: list
    else_body: list


@dataclass
class Block(Node):
    """优化器引入的显式作用域块：拼接常量条件的分支时保持 let 作用域。"""
    body: list


@dataclass
class Print(Node):
    value: Node


@dataclass
class ExprStmt(Node):
    value: Node


@dataclass
class Program(Node):
    body: list
