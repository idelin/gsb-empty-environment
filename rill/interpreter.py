"""Tree-walking interpreter for Rill.

Records are Python dicts passed by reference, so writes through a
``writable`` parameter are visible at the call site.  Every runtime fault
carries the offending expression's UTF-8 byte span.
"""

from __future__ import annotations

from typing import Any

from . import ast_nodes as ast
from .errors import EvalError


class ReturnSignal(Exception):
    def __init__(self, value: Any):
        self.value = value


class Cell:
    """Mutable storage slot bound to a variable or a writable field alias."""

    __slots__ = ("get", "set")

    def __init__(self, get, set):
        self.get = get
        self.set = set


class Env:
    def __init__(self, parent: "Env | None" = None):
        self.parent = parent
        self.cells: dict[str, Cell] = {}

    def define(self, name: str, cell: Cell) -> None:
        self.cells[name] = cell

    def find(self, name: str) -> Cell:
        env: Env | None = self
        while env is not None:
            if name in env.cells:
                return env.cells[name]
            env = env.parent
        raise KeyError(name)


def render(value: Any) -> str:
    if value is None:
        return "()"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, str):
        return value
    if isinstance(value, dict):
        parts = [f"{k}: {render(value[k])}" for k in sorted(value)]
        return "{" + ", ".join(parts) + "}"
    return str(value)


class Interpreter:
    def __init__(self, program: ast.Program):
        self.program = program
        self.output: list[str] = []

    def run(self) -> str:
        env = Env()
        self.exec_block(self.program.main, env)
        return "".join(self.output)

    # -- statements ---------------------------------------------------------

    def exec_block(self, block: ast.Block, parent: Env) -> None:
        env = Env(parent)
        for stmt in block.stmts:
            self.exec_stmt(stmt, env)

    def exec_stmt(self, stmt: ast.Stmt, env: Env) -> None:
        if isinstance(stmt, ast.Let):
            value = self.eval(stmt.value, env)
            env.define(stmt.name, self.local_cell(value))
        elif isinstance(stmt, ast.Assign):
            self.do_assign(stmt.target, self.eval(stmt.value, env), env)
        elif isinstance(stmt, ast.Extend):
            self.do_extend(stmt, env)
        elif isinstance(stmt, ast.If):
            if self.eval(stmt.cond, env):
                self.exec_block(stmt.then_block, env)
            elif stmt.else_block is not None:
                self.exec_block(stmt.else_block, env)
        elif isinstance(stmt, ast.While):
            while self.eval(stmt.cond, env):
                self.exec_block(stmt.body, env)
        elif isinstance(stmt, ast.Return):
            raise ReturnSignal(self.eval(stmt.value, env) if stmt.value else None)
        elif isinstance(stmt, ast.ExprStmt):
            self.eval(stmt.expr, env)
        elif isinstance(stmt, ast.Block):
            self.exec_block(stmt, env)
        else:
            raise AssertionError(f"unhandled statement {type(stmt)}")

    @staticmethod
    def local_cell(value: Any) -> Cell:
        box = [value]
        return Cell(lambda: box[0], lambda v: box.__setitem__(0, v))

    def lvalue_cell(self, target: ast.Expr, env: Env) -> Cell:
        if isinstance(target, ast.Var):
            try:
                return env.find(target.name)
            except KeyError:
                raise EvalError(f"unknown variable {target.name!r}", *target.span)
        if isinstance(target, ast.Field):
            record = self.eval(target.target, env)
            if not isinstance(record, dict):
                raise EvalError("field access on a non-record", *target.target.span)
            name = target.name
            return Cell(lambda r=record: r.get(name),
                        lambda v, r=record: r.__setitem__(name, v))
        raise EvalError("invalid assignment target", *target.span)

    def do_assign(self, target: ast.Expr, value: Any, env: Env) -> None:
        self.lvalue_cell(target, env).set(value)

    def do_extend(self, stmt: ast.Extend, env: Env) -> None:
        record = self.eval(stmt.target, env)
        if not isinstance(record, dict):
            raise EvalError("can only extend records", *stmt.target.span)
        if stmt.field in record:
            raise EvalError(
                f"field {stmt.field!r} already exists at runtime", *stmt.span)
        record[stmt.field] = self.eval(stmt.value, env)

    # -- expressions --------------------------------------------------------

    def eval(self, expr: ast.Expr, env: Env) -> Any:
        if isinstance(expr, ast.IntLit):
            return expr.value
        if isinstance(expr, ast.StrLit):
            return expr.value
        if isinstance(expr, ast.BoolLit):
            return expr.value
        if isinstance(expr, ast.Var):
            try:
                return env.find(expr.name).get()
            except KeyError:
                raise EvalError(f"unknown variable {expr.name!r}", *expr.span)
        if isinstance(expr, ast.RecordLit):
            return {name: self.eval(fexpr, env) for name, fexpr in expr.fields}
        if isinstance(expr, ast.Field):
            return self.eval_field(expr, env)
        if isinstance(expr, ast.Unary):
            value = self.eval(expr.operand, env)
            if expr.op == "!":
                return not value
            return -value
        if isinstance(expr, ast.Binary):
            return self.eval_binary(expr, env)
        if isinstance(expr, ast.Call):
            return self.eval_call(expr, env)
        raise AssertionError(f"unhandled expression {type(expr)}")

    def eval_field(self, expr: ast.Field, env: Env) -> Any:
        record = self.eval(expr.target, env)
        if not isinstance(record, dict):
            raise EvalError("field access on a non-record", *expr.target.span)
        if expr.name not in record:
            raise EvalError(f"missing field {expr.name!r} at runtime", *expr.span)
        return record[expr.name]

    def eval_binary(self, expr: ast.Binary, env: Env) -> Any:
        op = expr.op
        if op == "&&":
            return self.eval(expr.left, env) and self.eval(expr.right, env)
        if op == "||":
            return self.eval(expr.left, env) or self.eval(expr.right, env)
        left = self.eval(expr.left, env)
        right = self.eval(expr.right, env)
        try:
            if op == "+":
                return left + right
            if op == "-":
                return left - right
            if op == "*":
                return left * right
            if op == "/":
                return left // right
            if op == "%":
                return left % right
            if op == "<":
                return left < right
            if op == ">":
                return left > right
            if op == "<=":
                return left <= right
            if op == ">=":
                return left >= right
            if op == "==":
                return left == right
            if op == "!=":
                return left != right
        except (TypeError, ZeroDivisionError) as exc:
            raise EvalError(str(exc), *expr.span)
        raise AssertionError(op)

    def eval_call(self, expr: ast.Call, env: Env) -> Any:
        if expr.callee == "print":
            value = self.eval(expr.args[0], env)
            self.output.append(render(value) + "\n")
            return None
        fn = self.program.functions.get(expr.callee)
        if fn is None:
            raise EvalError(f"unknown function {expr.callee!r}()", *expr.span)
        call_env = Env()
        for arg_expr, param in zip(expr.args, fn.params):
            if param.mode == "readonly":
                call_env.define(param.name, self.local_cell(self.eval(arg_expr, env)))
            else:
                call_env.define(param.name, self.lvalue_cell(arg_expr, env))
        try:
            self.exec_block(fn.body, call_env)
        except ReturnSignal as ret:
            return ret.value
        return None
