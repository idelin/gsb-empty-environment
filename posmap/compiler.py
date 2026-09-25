"""AST -> 栈式字节码。

每条指令记录它在展开文本中的来源区间；没有来源的指令（如末尾生成的
HALT）span 为 None，诊断时绝不伪造行号。
"""
from __future__ import annotations

from dataclasses import dataclass

from .intervals import Span
from . import parser as ast


@dataclass
class Instr:
    op: str
    arg: object = None
    span: Span | None = None


_BINOPS = {"+": "ADD", "-": "SUB", "*": "MUL", "/": "DIV"}


def _gen(expr, out):
    if isinstance(expr, ast.Num):
        out.append(Instr("PUSH", expr.value, expr.span))
    elif isinstance(expr, ast.Var):
        out.append(Instr("LOAD", expr.name, expr.span))
    elif isinstance(expr, ast.Neg):
        _gen(expr.operand, out)
        out.append(Instr("NEG", None, expr.op_span))
    elif isinstance(expr, ast.BinOp):
        _gen(expr.left, out)
        _gen(expr.right, out)
        out.append(Instr(_BINOPS[expr.op], None, expr.op_span))
    else:
        raise TypeError(f"未知表达式: {expr!r}")


def compile_statement(stmt) -> list:
    out = []
    if isinstance(stmt, ast.LetStmt):
        _gen(stmt.expr, out)
        out.append(Instr("STORE", stmt.name, stmt.name_span))
    elif isinstance(stmt, ast.PrintStmt):
        _gen(stmt.expr, out)
        out.append(Instr("PRINT", None, stmt.kw_span))
    else:
        raise TypeError(f"未知语句: {stmt!r}")
    return out
