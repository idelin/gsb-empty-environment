"""Tree-walking interpreter for minilang programs."""
from __future__ import annotations

from .ast_nodes import (
    Assign,
    BinOp,
    Expr,
    Halt,
    If,
    IntLit,
    Let,
    Print,
    Stmt,
    Unary,
    Var,
    While,
)

MAX_STEPS = 1_000_000


class HaltSignal(Exception):
    pass


class RuntimeFailure(Exception):
    def __init__(self, message: str, start: int, end: int):
        super().__init__(message)
        self.start = start
        self.end = end


def _binop(op: str, left: int, right: int, node: Expr) -> int:
    if op == "+":
        return left + right
    if op == "-":
        return left - right
    if op == "*":
        return left * right
    if op in ("/", "%"):
        if right == 0:
            raise RuntimeFailure("division by zero", node.start, node.end)
        quotient = abs(left) // abs(right)
        if (left < 0) != (right < 0):
            quotient = -quotient
        if op == "/":
            return quotient
        return left - quotient * right
    if op == "==":
        return 1 if left == right else 0
    if op == "!=":
        return 1 if left != right else 0
    if op == "<":
        return 1 if left < right else 0
    if op == "<=":
        return 1 if left <= right else 0
    if op == ">":
        return 1 if left > right else 0
    if op == ">=":
        return 1 if left >= right else 0
    raise AssertionError(f"unknown operator {op!r}")


def eval_expr(node: Expr, env: dict[str, int]) -> int:
    if isinstance(node, IntLit):
        return node.value
    if isinstance(node, Var):
        try:
            return env[node.name]
        except KeyError:
            raise RuntimeFailure(
                f"undefined variable {node.name!r}", node.start, node.end
            ) from None
    if isinstance(node, Unary):
        value = eval_expr(node.operand, env)
        return -value if node.op == "-" else (0 if value else 1)
    if isinstance(node, BinOp):
        left = eval_expr(node.left, env)
        right = eval_expr(node.right, env)
        return _binop(node.op, left, right, node)
    raise AssertionError(f"unknown expression {node!r}")


class _NonConst(Exception):
    pass


def _const(node: Expr) -> int:
    if isinstance(node, IntLit):
        return node.value
    if isinstance(node, Var):
        raise _NonConst
    if isinstance(node, Unary):
        value = _const(node.operand)
        return -value if node.op == "-" else (0 if value else 1)
    if isinstance(node, BinOp):
        return _binop(node.op, _const(node.left), _const(node.right), node)
    raise _NonConst


def const_value(node: Expr) -> int | None:
    """Constant-fold a variable-free expression; None if not constant."""
    try:
        return _const(node)
    except (_NonConst, RuntimeFailure):
        return None


class _Executor:
    def __init__(self, emit):
        self.env: dict[str, int] = {}
        self.emit = emit
        self.steps = 0

    def tick(self, stmt: Stmt) -> None:
        self.steps += 1
        if self.steps > MAX_STEPS:
            raise RuntimeFailure("step limit exceeded", stmt.start, stmt.end)

    def exec_block(self, stmts: list[Stmt]) -> None:
        for stmt in stmts:
            self.exec_stmt(stmt)

    def exec_stmt(self, stmt: Stmt) -> None:
        self.tick(stmt)
        if isinstance(stmt, (Let, Assign)):
            self.env[stmt.name] = eval_expr(stmt.value, self.env)
        elif isinstance(stmt, Print):
            self.emit(str(eval_expr(stmt.value, self.env)))
        elif isinstance(stmt, If):
            if eval_expr(stmt.cond, self.env):
                self.exec_block(stmt.then_body)
            else:
                self.exec_block(stmt.else_body)
        elif isinstance(stmt, While):
            while eval_expr(stmt.cond, self.env):
                self.exec_block(stmt.body)
                self.tick(stmt)
        elif isinstance(stmt, Halt):
            raise HaltSignal
        else:
            raise AssertionError(f"unknown statement {stmt!r}")


def run(program: list[Stmt], emit=print) -> None:
    executor = _Executor(emit)
    try:
        executor.exec_block(program)
    except HaltSignal:
        pass
