"""手写树遍历解释器。整数语义，非零为真。"""
from __future__ import annotations

from .nodes import (
    Assign, Bin, Expr, If, Neg, Num, Print, Program, Stmt, Var, While,
)


class InterpError(Exception):
    def __init__(self, message: str, span: tuple[int, int]):
        super().__init__(message)
        self.span = span


class Interpreter:
    def __init__(self, out=None):
        self.out = out if out is not None else print
        self.env: dict[str, int] = {}

    def run(self, program: Program) -> None:
        self.exec_block(program.body)

    def exec_block(self, stmts: list[Stmt]) -> None:
        for stmt in stmts:
            self.exec_stmt(stmt)

    def exec_stmt(self, stmt: Stmt) -> None:
        if isinstance(stmt, Assign):
            self.env[stmt.name] = self.eval(stmt.value)
        elif isinstance(stmt, Print):
            self.out(str(self.eval(stmt.value)))
        elif isinstance(stmt, If):
            if self.eval(stmt.cond) != 0:
                self.exec_block(stmt.then_body)
            else:
                self.exec_block(stmt.else_body)
        elif isinstance(stmt, While):
            while self.eval(stmt.cond) != 0:
                self.exec_block(stmt.body)
        else:
            raise TypeError(f"未知语句类型: {type(stmt).__name__}")

    def eval(self, expr: Expr) -> int:
        if isinstance(expr, Num):
            return expr.value
        if isinstance(expr, Var):
            if expr.name not in self.env:
                raise InterpError(f"变量 {expr.name!r} 未定义", expr.span)
            return self.env[expr.name]
        if isinstance(expr, Neg):
            return -self.eval(expr.operand)
        if isinstance(expr, Bin):
            left = self.eval(expr.left)
            right = self.eval(expr.right)
            if expr.op == "+":
                return left + right
            if expr.op == "-":
                return left - right
            if expr.op == "*":
                return left * right
            if expr.op in ("/", "%"):
                if right == 0:
                    raise InterpError("除数为零", expr.span)
                quot = abs(left) // abs(right)
                quot = quot if (left < 0) == (right < 0) else -quot
                return quot if expr.op == "/" else left - quot * right
            if expr.op == "==":
                return int(left == right)
            if expr.op == "!=":
                return int(left != right)
            if expr.op == "<":
                return int(left < right)
            if expr.op == "<=":
                return int(left <= right)
            if expr.op == ">":
                return int(left > right)
            if expr.op == ">=":
                return int(left >= right)
            raise InterpError(f"未知运算符 {expr.op!r}", expr.span)
        raise TypeError(f"未知表达式类型: {type(expr).__name__}")
