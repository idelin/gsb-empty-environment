"""Tree-walking interpreter.

Control-transfer semantics (the interesting part):

* ``break`` / ``continue`` / ``return`` / ``throw`` are all represented as
  :class:`~minilang.errors.ControlTransfer` exceptions, so a pending
  transfer is a single uniform object.
* ``finally`` ALWAYS runs, no matter how the try (and catch) completed.
* If the ``finally`` block itself completes abruptly with a new transfer
  T_new (throw / return / break / continue), T_new *overrides* any pending
  transfer T_old.  This is the single, explicit priority rule:
  "finally's own transfer wins".  If ``finally`` completes normally, the
  pending transfer (if any) continues to propagate unchanged.
* When T_new is a throw and T_old was also a throw, the original error is
  preserved as ``T_new.cause`` (a chain, for nested overrides).
* ``return`` evaluates its expression BEFORE any ``finally`` runs, so the
  already-computed value is returned even if ``finally`` mutates the
  variables it was computed from.
* Nested ``try`` statements unwind inside-out: inner ``finally`` blocks run
  before outer ones, each getting its own chance to override.
"""
from __future__ import annotations

from .ast_nodes import (
    Assign,
    Binary,
    Block,
    BreakStmt,
    Call,
    ContinueStmt,
    ExprStmt,
    ForStmt,
    FuncDecl,
    IfStmt,
    LetStmt,
    Literal,
    Logical,
    ReturnStmt,
    ThrowStmt,
    TryStmt,
    Unary,
    Var,
    WhileStmt,
)
from .errors import (
    BreakSignal,
    ContinueSignal,
    ControlTransfer,
    LangThrow,
    ReturnSignal,
    RuntimeFault,
)


# ---------------------------------------------------------------------------
# Values
# ---------------------------------------------------------------------------


def is_truthy(value) -> bool:
    """Only ``nil`` and ``false`` are falsy."""
    if value is None or value is False:
        return False
    return True


def lang_equals(a, b) -> bool:
    if isinstance(a, bool) or isinstance(b, bool):
        return isinstance(a, bool) and isinstance(b, bool) and a == b
    if isinstance(a, (int, float)) and isinstance(b, (int, float)):
        return a == b
    if type(a) is not type(b):
        return False
    return a == b


def stringify(value) -> str:
    if value is None:
        return "nil"
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


def _is_number(value) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool)


class Function:
    """A user-defined function closing over its defining environment."""

    def __init__(self, decl: FuncDecl, closure: "Environment"):
        self.decl = decl
        self.closure = closure


class Builtin:
    def __init__(self, name, fn):
        self.name = name
        self.fn = fn


class Environment:
    __slots__ = ("values", "parent")

    def __init__(self, parent: "Environment | None" = None):
        self.values: dict = {}
        self.parent = parent

    def define(self, name: str, value):
        self.values[name] = value

    def get(self, name: str, node):
        env = self
        while env is not None:
            if name in env.values:
                return env.values[name]
            env = env.parent
        raise RuntimeFault(f"undefined variable '{name}'", node.start, node.end)

    def assign(self, name: str, value, node):
        env = self
        while env is not None:
            if name in env.values:
                env.values[name] = value
                return
            env = env.parent
        raise RuntimeFault(
            f"cannot assign to undefined variable '{name}'", node.start, node.end
        )


# ---------------------------------------------------------------------------
# Interpreter
# ---------------------------------------------------------------------------


class Interpreter:
    def __init__(self, out=None):
        self.globals = Environment()
        self._out = out if out is not None else (lambda text: print(text))
        self.globals.define("print", Builtin("print", self._builtin_print))

    # -- entry point ----------------------------------------------------

    def interpret(self, program):
        try:
            for stmt in program.stmts:
                self.execute(stmt, self.globals)
        except BreakSignal as sig:
            raise RuntimeFault("'break' outside of a loop", sig.start, sig.end) from None
        except ContinueSignal as sig:
            raise RuntimeFault(
                "'continue' outside of a loop", sig.start, sig.end
            ) from None
        except ReturnSignal as sig:
            raise RuntimeFault(
                "'return' outside of a function", sig.start, sig.end
            ) from None
        # LangThrow propagates to the caller as the uncaught error.

    # -- statement execution --------------------------------------------

    def execute(self, node, env: Environment):
        handler = self._exec_handlers.get(type(node))
        if handler is None:  # pragma: no cover - defensive
            raise AssertionError(f"cannot execute {type(node).__name__}")
        handler(self, node, env)

    def _exec_let(self, node: LetStmt, env):
        value = self.evaluate(node.init, env) if node.init is not None else None
        env.define(node.name, value)

    def _exec_expr_stmt(self, node: ExprStmt, env):
        self.evaluate(node.expr, env)

    def _exec_block(self, node: Block, env):
        block_env = Environment(env)
        for stmt in node.stmts:
            self.execute(stmt, block_env)

    def _exec_if(self, node: IfStmt, env):
        if is_truthy(self.evaluate(node.cond, env)):
            self.execute(node.then_branch, env)
        elif node.else_branch is not None:
            self.execute(node.else_branch, env)

    def _exec_while(self, node: WhileStmt, env):
        while is_truthy(self.evaluate(node.cond, env)):
            try:
                self.execute(node.body, env)
            except BreakSignal:
                break
            except ContinueSignal:
                continue

    def _exec_for(self, node: ForStmt, env):
        loop_env = Environment(env)
        if node.init is not None:
            self.execute(node.init, loop_env)
        while True:
            if node.cond is not None and not is_truthy(
                self.evaluate(node.cond, loop_env)
            ):
                break
            try:
                self.execute(node.body, loop_env)
            except BreakSignal:
                break
            except ContinueSignal:
                pass
            if node.step is not None:
                self.evaluate(node.step, loop_env)

    def _exec_break(self, node: BreakStmt, env):
        raise BreakSignal(node.start, node.end)

    def _exec_continue(self, node: ContinueStmt, env):
        raise ContinueSignal(node.start, node.end)

    def _exec_return(self, node: ReturnStmt, env):
        # The value is fully evaluated HERE, before any finally block runs.
        value = self.evaluate(node.value, env) if node.value is not None else None
        raise ReturnSignal(value, node.start, node.end)

    def _exec_throw(self, node: ThrowStmt, env):
        value = self.evaluate(node.value, env)
        raise LangThrow(value, node.start, node.end)

    def _exec_func_decl(self, node: FuncDecl, env):
        env.define(node.name, Function(node, env))

    def _exec_try(self, node: TryStmt, env):
        pending: ControlTransfer | None = None

        # 1. Run the try body, remembering how it completed.
        try:
            self._exec_block(node.body, env)
        except ControlTransfer as transfer:
            pending = transfer

        # 2. The catch clause only handles thrown values; any other pending
        #    transfer (break / continue / return) passes straight through.
        if node.catch_body is not None and isinstance(pending, LangThrow):
            catch_env = Environment(env)
            catch_env.define(node.catch_param, pending.value)
            try:
                self._exec_block(node.catch_body, catch_env)
            except ControlTransfer as transfer:
                pending = transfer
            else:
                pending = None

        # 3. finally ALWAYS runs.  If it completes abruptly with a new
        #    transfer, that transfer OVERRIDES whatever was pending
        #    (explicit priority: finally's own transfer wins).  A new throw
        #    keeps the overridden throw as its ``cause``.
        if node.finally_body is not None:
            try:
                self._exec_block(node.finally_body, env)
            except ControlTransfer as new_transfer:
                if (
                    isinstance(new_transfer, LangThrow)
                    and isinstance(pending, LangThrow)
                    and new_transfer.cause is None
                ):
                    new_transfer.cause = pending
                pending = new_transfer

        # 4. Re-raise whatever survived.
        if pending is not None:
            raise pending

    # -- expression evaluation ------------------------------------------

    def evaluate(self, node, env: Environment):
        handler = self._eval_handlers.get(type(node))
        if handler is None:  # pragma: no cover - defensive
            raise AssertionError(f"cannot evaluate {type(node).__name__}")
        return handler(self, node, env)

    def _eval_literal(self, node: Literal, env):
        return node.value

    def _eval_var(self, node: Var, env):
        return env.get(node.name, node)

    def _eval_assign(self, node: Assign, env):
        value = self.evaluate(node.value, env)
        env.assign(node.name, value, node)
        return value

    def _eval_unary(self, node: Unary, env):
        operand = self.evaluate(node.operand, env)
        if node.op == "MINUS":
            self._check_number(operand, node, "operand of unary '-'")
            return -operand
        # BANG
        return not is_truthy(operand)

    def _eval_logical(self, node: Logical, env):
        left = self.evaluate(node.left, env)
        if node.op == "OR":
            if is_truthy(left):
                return left
        else:  # AND
            if not is_truthy(left):
                return left
        return self.evaluate(node.right, env)

    def _eval_binary(self, node: Binary, env):
        left = self.evaluate(node.left, env)
        right = self.evaluate(node.right, env)
        op = node.op

        if op == "EQUAL_EQUAL":
            return lang_equals(left, right)
        if op == "BANG_EQUAL":
            return not lang_equals(left, right)

        if op == "PLUS":
            if _is_number(left) and _is_number(right):
                return left + right
            if isinstance(left, str) and isinstance(right, str):
                return left + right
            raise RuntimeFault(
                "operands of '+' must be two numbers or two strings",
                node.start,
                node.end,
            )

        if op in ("MINUS", "STAR", "SLASH", "PERCENT"):
            self._check_number(left, node, f"left operand of '{self._op_text(op)}'")
            self._check_number(right, node, f"right operand of '{self._op_text(op)}'")
            if op == "MINUS":
                return left - right
            if op == "STAR":
                return left * right
            if op == "SLASH":
                if right == 0:
                    raise RuntimeFault("division by zero", node.start, node.end)
                return left / right
            # PERCENT
            if right == 0:
                raise RuntimeFault("modulo by zero", node.start, node.end)
            return left % right

        # comparisons
        self._check_number(left, node, f"left operand of '{self._op_text(op)}'")
        self._check_number(right, node, f"right operand of '{self._op_text(op)}'")
        if op == "LESS":
            return left < right
        if op == "LESS_EQUAL":
            return left <= right
        if op == "GREATER":
            return left > right
        return left >= right  # GREATER_EQUAL

    def _eval_call(self, node: Call, env):
        callee = self.evaluate(node.callee, env)
        args = [self.evaluate(arg, env) for arg in node.args]

        if isinstance(callee, Builtin):
            return callee.fn(args, node)

        if isinstance(callee, Function):
            expected = len(callee.decl.params)
            if len(args) != expected:
                raise RuntimeFault(
                    f"function '{callee.decl.name}' expects {expected} "
                    f"argument(s), got {len(args)}",
                    node.start,
                    node.end,
                )
            call_env = Environment(callee.closure)
            for param, arg in zip(callee.decl.params, args):
                call_env.define(param, arg)
            try:
                self._exec_block(callee.decl.body, call_env)
            except ReturnSignal as ret:
                return ret.value
            return None

        raise RuntimeFault("only functions can be called", node.start, node.end)

    # -- helpers ----------------------------------------------------------

    @staticmethod
    def _check_number(value, node, what):
        if not _is_number(value):
            raise RuntimeFault(
                f"{what} must be a number, got {stringify(value)}",
                node.start,
                node.end,
            )

    @staticmethod
    def _op_text(op):
        return {
            "MINUS": "-",
            "STAR": "*",
            "SLASH": "/",
            "PERCENT": "%",
            "LESS": "<",
            "LESS_EQUAL": "<=",
            "GREATER": ">",
            "GREATER_EQUAL": ">=",
        }.get(op, op)

    def _builtin_print(self, args, node):
        self._out(" ".join(stringify(arg) for arg in args))
        return None


# Dispatch tables (filled in after class definition).
Interpreter._exec_handlers = {
    LetStmt: Interpreter._exec_let,
    ExprStmt: Interpreter._exec_expr_stmt,
    Block: Interpreter._exec_block,
    IfStmt: Interpreter._exec_if,
    WhileStmt: Interpreter._exec_while,
    ForStmt: Interpreter._exec_for,
    BreakStmt: Interpreter._exec_break,
    ContinueStmt: Interpreter._exec_continue,
    ReturnStmt: Interpreter._exec_return,
    ThrowStmt: Interpreter._exec_throw,
    FuncDecl: Interpreter._exec_func_decl,
    TryStmt: Interpreter._exec_try,
}

Interpreter._eval_handlers = {
    Literal: Interpreter._eval_literal,
    Var: Interpreter._eval_var,
    Assign: Interpreter._eval_assign,
    Unary: Interpreter._eval_unary,
    Logical: Interpreter._eval_logical,
    Binary: Interpreter._eval_binary,
    Call: Interpreter._eval_call,
}
