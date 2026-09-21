"""Tree-walking interpreter.

Control transfers (break / continue / return / throw) are modelled as
Python exceptions ("signals").  Each signal carries a priority; when a
``finally`` block produces a new transfer while an older one is still
pending, the two are resolved by ``resolve_transfer``:

    throw (4) > return (3) > break (2) > continue (1) > normal (0)

The transfer with the higher priority wins; on a tie the *newer*
transfer (the one from ``finally``) wins.  When a throw overrides an
earlier throw, the earlier one is kept as the new one's ``cause``.
"""

from __future__ import annotations

import sys

from . import ast_nodes as ast
from .errors import LangError


# ---------- control-transfer signals ----------

class Signal(Exception):
    priority = 0


class ContinueSignal(Signal):
    priority = 1


class BreakSignal(Signal):
    priority = 2


class ReturnSignal(Signal):
    priority = 3

    def __init__(self, value):
        super().__init__("return outside function")
        self.value = value


class ThrowSignal(Signal):
    priority = 4

    def __init__(self, value, span, cause=None):
        super().__init__(f"uncaught throw: {show(value)}")
        self.value = value
        self.span = span
        self.cause = cause


def resolve_transfer(old: Signal | None, new: Signal) -> Signal:
    """Resolve a pending transfer against a newer one from ``finally``."""
    if old is None or new.priority >= old.priority:
        if isinstance(new, ThrowSignal) and isinstance(old, ThrowSignal):
            # Keep the overridden error reachable: append it at the end of
            # the new throw's cause chain.
            tail = new
            while tail.cause is not None:
                tail = tail.cause
            tail.cause = old
        return new
    return old


# ---------- values ----------

def truthy(value) -> bool:
    if value is None or value is False:
        return False
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return value != 0
    if isinstance(value, str):
        return value != ""
    return True


def show(value) -> str:
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, float) and value.is_integer():
        return str(int(value))
    if isinstance(value, Function):
        return f"<fn {value.decl.name}>"
    if isinstance(value, Builtin):
        return f"<builtin {value.name}>"
    return str(value)


def lang_eq(a, b) -> bool:
    if isinstance(a, bool) or isinstance(b, bool):
        return a is b
    return a == b


class Env:
    def __init__(self, parent: "Env | None" = None):
        self.vars: dict[str, object] = {}
        self.parent = parent

    def define(self, name: str, value, span=None):
        if name in self.vars:
            raise LangError(f"redefinition of '{name}'", span)
        self.vars[name] = value

    def get(self, name: str, span):
        env = self
        while env is not None:
            if name in env.vars:
                return env.vars[name]
            env = env.parent
        raise LangError(f"undefined variable '{name}'", span)

    def set(self, name: str, value, span):
        env = self
        while env is not None:
            if name in env.vars:
                env.vars[name] = value
                return
            env = env.parent
        raise LangError(f"assignment to undefined variable '{name}'", span)


class Function:
    def __init__(self, decl: ast.FuncDef, closure: Env):
        self.decl = decl
        self.closure = closure

    def call(self, interp: "Interpreter", args: list, span):
        if len(args) != len(self.decl.params):
            raise LangError(
                f"'{self.decl.name}' expects {len(self.decl.params)} "
                f"argument(s), got {len(args)}", span)
        env = Env(self.closure)
        for param, arg in zip(self.decl.params, args):
            env.define(param, arg)
        saved_depth = interp.loop_depth
        interp.loop_depth = 0
        try:
            interp.exec_block(self.decl.body, env, new_scope=False)
        except ReturnSignal as ret:
            return ret.value
        finally:
            interp.loop_depth = saved_depth
        return None


class Builtin:
    def __init__(self, name: str, fn):
        self.name = name
        self.fn = fn

    def call(self, interp: "Interpreter", args: list, span):
        return self.fn(interp, args, span)


# ---------- interpreter ----------

SIGNALS = (BreakSignal, ContinueSignal, ReturnSignal, ThrowSignal)


class Interpreter:
    def __init__(self, out=None):
        self.out = out if out is not None else sys.stdout
        self.loop_depth = 0
        self.global_env = Env()
        self.global_env.define("print", Builtin("print", self._builtin_print))
        self.global_env.define("len", Builtin("len", self._builtin_len))

    # builtins

    def _builtin_print(self, interp, args, span):
        self.out.write(" ".join(show(a) for a in args) + "\n")
        return None

    def _builtin_len(self, interp, args, span):
        if len(args) != 1 or not isinstance(args[0], str):
            raise LangError("len expects a single string argument", span)
        return len(args[0])

    # entry point

    def run(self, program: ast.Program):
        for stmt in program.body:
            self.exec_stmt(stmt, self.global_env)

    # ---------- statements ----------

    def exec_block(self, block: ast.Block, env: Env, new_scope: bool = True):
        scope = Env(env) if new_scope else env
        for stmt in block.body:
            self.exec_stmt(stmt, scope)

    def exec_stmt(self, node: ast.Node, env: Env):
        if isinstance(node, ast.Block):
            self.exec_block(node, env)
        elif isinstance(node, ast.Let):
            env.define(node.name, self.eval_expr(node.init, env),
                       (node.start, node.end))
        elif isinstance(node, ast.Assign):
            env.set(node.name, self.eval_expr(node.value, env),
                    (node.start, node.end))
        elif isinstance(node, ast.ExprStmt):
            self.eval_expr(node.expr, env)
        elif isinstance(node, ast.If):
            if truthy(self.eval_expr(node.cond, env)):
                self.exec_block(node.then, env)
            elif node.otherwise is not None:
                if isinstance(node.otherwise, ast.If):
                    self.exec_stmt(node.otherwise, env)
                else:
                    self.exec_block(node.otherwise, env)
        elif isinstance(node, ast.While):
            self.exec_while(node, env)
        elif isinstance(node, ast.FuncDef):
            env.define(node.name, Function(node, env), (node.start, node.end))
        elif isinstance(node, ast.Return):
            # The value is evaluated *now*; a finally block that later
            # mutates outer state cannot change this snapshot.
            value = self.eval_expr(node.value, env) if node.value else None
            raise ReturnSignal(value)
        elif isinstance(node, ast.Break):
            if self.loop_depth == 0:
                raise LangError("break outside loop",
                                (node.start, node.end))
            raise BreakSignal()
        elif isinstance(node, ast.Continue):
            if self.loop_depth == 0:
                raise LangError("continue outside loop",
                                (node.start, node.end))
            raise ContinueSignal()
        elif isinstance(node, ast.Throw):
            value = self.eval_expr(node.value, env)
            raise ThrowSignal(value, (node.start, node.end))
        elif isinstance(node, ast.Try):
            self.exec_try(node, env)
        else:  # pragma: no cover - defensive
            raise LangError(f"cannot execute {type(node).__name__}",
                            (node.start, node.end))

    def exec_while(self, node: ast.While, env: Env):
        self.loop_depth += 1
        try:
            while truthy(self.eval_expr(node.cond, env)):
                try:
                    self.exec_block(node.body, env)
                except BreakSignal:
                    break
                except ContinueSignal:
                    continue
        finally:
            self.loop_depth -= 1

    def exec_try(self, node: ast.Try, env: Env):
        pending: Signal | None = None
        try:
            self.exec_block(node.body, env)
        except SIGNALS as sig:
            pending = sig

        if node.catch_body is not None and isinstance(pending, ThrowSignal):
            catch_env = Env(env)
            catch_env.define(node.catch_name, pending.value)
            try:
                self.exec_block(node.catch_body, catch_env, new_scope=False)
                pending = None
            except SIGNALS as sig:
                pending = sig

        # finally always runs, whatever is pending.
        if node.finally_body is not None:
            try:
                self.exec_block(node.finally_body, env)
            except SIGNALS as sig:
                pending = resolve_transfer(pending, sig)

        if pending is not None:
            raise pending

    # ---------- expressions ----------

    def eval_expr(self, node: ast.Node, env: Env):
        if isinstance(node, ast.Literal):
            return node.value
        if isinstance(node, ast.Var):
            return env.get(node.name, (node.start, node.end))
        if isinstance(node, ast.Logical):
            left = self.eval_expr(node.left, env)
            if node.op == "&&":
                return self.eval_expr(node.right, env) if truthy(left) else left
            return left if truthy(left) else self.eval_expr(node.right, env)
        if isinstance(node, ast.Unary):
            return self.eval_unary(node, env)
        if isinstance(node, ast.Binary):
            return self.eval_binary(node, env)
        if isinstance(node, ast.Call):
            return self.eval_call(node, env)
        raise LangError(f"cannot evaluate {type(node).__name__}",
                        (node.start, node.end))  # pragma: no cover

    def eval_unary(self, node: ast.Unary, env: Env):
        value = self.eval_expr(node.operand, env)
        if node.op == "!":
            return not truthy(value)
        if node.op == "-":
            if isinstance(value, bool) or not isinstance(value, (int, float)):
                raise LangError("unary '-' expects a number",
                                (node.start, node.end))
            return -value
        raise LangError(f"unknown unary operator {node.op!r}",
                        (node.start, node.end))  # pragma: no cover

    def eval_binary(self, node: ast.Binary, env: Env):
        left = self.eval_expr(node.left, env)
        right = self.eval_expr(node.right, env)
        span = (node.start, node.end)
        op = node.op

        def number(v):
            return isinstance(v, (int, float)) and not isinstance(v, bool)

        if op == "+":
            if number(left) and number(right):
                return left + right
            if isinstance(left, str) and isinstance(right, str):
                return left + right
            raise LangError("'+' expects two numbers or two strings", span)
        if op in ("-", "*", "/", "%"):
            if not (number(left) and number(right)):
                raise LangError(f"'{op}' expects two numbers", span)
            if op == "-":
                return left - right
            if op == "*":
                return left * right
            if op == "/":
                if right == 0:
                    raise LangError("division by zero", span)
                return left / right
            if right == 0:
                raise LangError("modulo by zero", span)
            return left % right
        if op in ("<", "<=", ">", ">="):
            both_num = number(left) and number(right)
            both_str = isinstance(left, str) and isinstance(right, str)
            if not (both_num or both_str):
                raise LangError(f"'{op}' expects two numbers or two strings",
                                span)
            if op == "<":
                return left < right
            if op == "<=":
                return left <= right
            if op == ">":
                return left > right
            return left >= right
        if op == "==":
            return lang_eq(left, right)
        if op == "!=":
            return not lang_eq(left, right)
        raise LangError(f"unknown operator {op!r}", span)  # pragma: no cover

    def eval_call(self, node: ast.Call, env: Env):
        callee = self.eval_expr(node.callee, env)
        args = [self.eval_expr(arg, env) for arg in node.args]
        span = (node.start, node.end)
        if isinstance(callee, (Function, Builtin)):
            return callee.call(self, args, span)
        raise LangError(f"cannot call {show(callee)}", span)
