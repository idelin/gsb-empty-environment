"""AST 节点定义。所有 span 均为 UTF-8 字节区间 [start, end)。"""
from __future__ import annotations

from dataclasses import dataclass

Span = tuple[int, int]  # (起始字节, 结束字节)，对源码做 UTF-8 编码后的偏移


# ---- 表达式 ----

@dataclass
class Num:
    value: int
    span: Span

@dataclass
class Var:
    name: str
    span: Span

@dataclass
class Neg:
    operand: Expr
    span: Span

@dataclass
class Bin:
    op: str  # + - * / % == != < <= > >=
    left: Expr
    right: Expr
    span: Span

Expr = Num | Var | Neg | Bin


# ---- 语句 ----

@dataclass
class Assign:
    name: str
    name_span: Span
    value: Expr
    span: Span

@dataclass
class Print:
    value: Expr
    span: Span

@dataclass
class If:
    cond: Expr
    then_body: list[Stmt]
    else_body: list[Stmt]  # 无 else 时为空列表
    span: Span

@dataclass
class While:
    cond: Expr
    body: list[Stmt]
    span: Span

Stmt = Assign | Print | If | While


@dataclass
class Program:
    body: list[Stmt]
    span: Span
