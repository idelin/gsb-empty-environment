"""Tree-walking interpreter with generator-based coroutines.

Every statement/expression evaluator is a Python generator. A ``yield``
expression in the language maps to a Python ``yield``, so suspending a
coroutine freezes the entire chain of interpreter frames (call stack,
loop counters, locals) inside the paused generators. Closing a
coroutine throws ``GeneratorExit`` at the suspension point, which runs
``finally`` blocks exactly once as the stack unwinds.
"""

from dataclasses import dataclass

from .errors import LangError, ReentrancyError
from .lexer import Lexer
from .nodes import (
    Assign, Binary, Call, ExprStmt, If, Let, Literal, Return, Throw,
    Try, Unary, Var, While, YieldExpr,
)
from .parser import Parser


class ReturnSignal(Exception):
    def __init__(self, value):
        super().__init__("return outside function")
        self.value = value


def truthy(value):
    if value is None or value is False:
        return False
    if isinstance(value, bool):
        return value
    if value == 0 or value == "":
        return False
    return True


def value_to_str(value):
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    return str(value)


def _values_equal(left, right):
    if isinstance(left, bool) or isinstance(right, bool):
        return left is right
    return left == right


class Frame:
    """One lexical scope; blocks create child frames linked by parent."""

    def __init__(self, parent=None):
        self.vars = {}
        self.parent = parent

    def define(self, name, value):
        self.vars[name] = value

    def lookup(self, name):
        frame = self
        while frame is not None:
            if name in frame.vars:
                return frame.vars[name]
            frame = frame.parent
        raise KeyError(name)

    def assign(self, name, value):
        frame = self
        while frame is not None:
            if name in frame.vars:
                frame.vars[name] = value
                return
            frame = frame.parent
        raise KeyError(name)

    def has(self, name):
        frame = self
        while frame is not None:
            if name in frame.vars:
                return True
            frame = frame.parent
        return False


@dataclass
class ResumeResult:
    """Outcome of resume/throw/close.

    ``status`` is one of "suspended", "returned", "closed", "failed".
    ``terminal`` is True once the coroutine can never run again.
    """

    status: str
    value: object = None
    terminal: bool = False


class Coroutine:
    """A single execution of a function, driven from host Python."""

    def __init__(self, interp, fn_def, args):
        self.interp = interp
        self.fn_name = fn_def.name
        self._gen = interp._run_function(fn_def, list(args), self)
        self.state = "suspended"   # suspended | running | returned | closed | failed
        self.return_value = None
        self.failure = None
        self._started = False

    @property
    def terminal(self):
        return self.state in ("returned", "closed", "failed")

    def _terminal_result(self):
        value = self.return_value if self.state == "returned" else None
        return ResumeResult(self.state, value, terminal=True)

    def resume(self, value=None):
        if self.state == "running":
            raise ReentrancyError(
                "coroutine %r is already running" % self.fn_name)
        if self.state != "suspended":
            return self._terminal_result()
        if not self._started and value is not None:
            raise ValueError("cannot send a value to a coroutine before its first yield")
        self.state = "running"
        try:
            if not self._started:
                self._started = True
                outcome = next(self._gen)
            else:
                outcome = self._gen.send(value)
        except StopIteration as stop:
            self.state = "returned"
            self.return_value = stop.value
            return ResumeResult("returned", stop.value, terminal=True)
        except BaseException as err:
            self.state = "failed"
            self.failure = err
            raise
        self.state = "suspended"
        return ResumeResult("suspended", outcome)

    def throw(self, value):
        """Inject a language-level exception at the suspension point."""
        if self.state == "running":
            raise ReentrancyError(
                "coroutine %r is already running" % self.fn_name)
        if self.state != "suspended":
            return self._terminal_result()
        err = LangError("uncaught throw: %s" % value_to_str(value), value=value)
        self.state = "running"
        try:
            outcome = self._gen.throw(err)
        except StopIteration as stop:
            self.state = "returned"
            self.return_value = stop.value
            return ResumeResult("returned", stop.value, terminal=True)
        except BaseException as exc:
            self.state = "failed"
            self.failure = exc
            raise
        self.state = "suspended"
        return ResumeResult("suspended", outcome)

    def close(self):
        """Close a suspended coroutine, running finally blocks once."""
        if self.state == "running":
            raise ReentrancyError(
                "coroutine %r is already running" % self.fn_name)
        if self.state != "suspended":
            return self._terminal_result()
        self.state = "running"
        try:
            self._gen.close()
        except RuntimeError as exc:
            self.state = "closed"
            raise LangError("coroutine yielded from finally during close") from exc
        except BaseException:
            self.state = "closed"
            raise
        self.state = "closed"
        return ResumeResult("closed", None, terminal=True)


class Interpreter:
    def __init__(self):
        self.functions = {}
        self.builtins = {}
        self.output = []
        self.define_builtin("print", lambda args, interp: interp._builtin_print(args))

    def load(self, source):
        program = Parser(Lexer(source).tokenize()).parse_program()
        for fn_def in program.defs:
            self.functions[fn_def.name] = fn_def
        return program

    def define_builtin(self, name, fn):
        """Register a host callable ``fn(args: list, interp) -> value``."""
        self.builtins[name] = fn

    def spawn(self, name, args=()):
        fn_def = self.functions.get(name)
        if fn_def is None:
            raise LangError("undefined function %r" % name)
        if len(args) != len(fn_def.params):
            raise LangError(
                "%s expects %d argument(s), got %d"
                % (name, len(fn_def.params), len(args)))
        return Coroutine(self, fn_def, args)

    def _builtin_print(self, args):
        self.output.append(" ".join(value_to_str(a) for a in args))
        return None

    # -- function execution ------------------------------------------------

    def _run_function(self, fn_def, args, co):
        frame = Frame()
        for param, arg in zip(fn_def.params, args):
            frame.define(param, arg)
        try:
            yield from self._exec_block(fn_def.body, frame, co)
        except ReturnSignal as ret:
            return ret.value
        return None

    def _exec_block(self, stmts, frame, co):
        for stmt in stmts:
            yield from self._exec_stmt(stmt, frame, co)

    def _exec_stmt(self, stmt, frame, co):
        if isinstance(stmt, Let):
            value = yield from self._eval(stmt.expr, frame, co)
            frame.define(stmt.name, value)
        elif isinstance(stmt, Assign):
            value = yield from self._eval(stmt.expr, frame, co)
            try:
                frame.assign(stmt.name, value)
            except KeyError:
                raise LangError(
                    "cannot assign to undefined variable %r" % stmt.name, stmt.span)
        elif isinstance(stmt, ExprStmt):
            yield from self._eval(stmt.expr, frame, co)
        elif isinstance(stmt, If):
            cond = yield from self._eval(stmt.cond, frame, co)
            if truthy(cond):
                yield from self._exec_block(stmt.then_body, Frame(frame), co)
            elif stmt.else_body is not None:
                yield from self._exec_block(stmt.else_body, Frame(frame), co)
        elif isinstance(stmt, While):
            while True:
                cond = yield from self._eval(stmt.cond, frame, co)
                if not truthy(cond):
                    break
                yield from self._exec_block(stmt.body, Frame(frame), co)
        elif isinstance(stmt, Return):
            value = None
            if stmt.expr is not None:
                value = yield from self._eval(stmt.expr, frame, co)
            raise ReturnSignal(value)
        elif isinstance(stmt, Throw):
            value = yield from self._eval(stmt.expr, frame, co)
            raise LangError(
                "uncaught throw: %s" % value_to_str(value), stmt.span, value=value)
        elif isinstance(stmt, Try):
            yield from self._exec_try(stmt, frame, co)
        else:
            raise LangError("unknown statement %r" % type(stmt).__name__, stmt.span)

    def _exec_try(self, stmt, frame, co):
        try:
            yield from self._exec_block(stmt.body, Frame(frame), co)
        except LangError as err:
            if stmt.catch_body is None:
                raise
            catch_frame = Frame(frame)
            catch_frame.define(stmt.catch_name, err.value)
            yield from self._exec_block(stmt.catch_body, catch_frame, co)
        finally:
            if stmt.finally_body is not None:
                yield from self._exec_block(stmt.finally_body, Frame(frame), co)

    # -- expression evaluation ----------------------------------------------

    def _eval(self, node, frame, co):
        if isinstance(node, Literal):
            return node.value
        if isinstance(node, Var):
            try:
                return frame.lookup(node.name)
            except KeyError:
                raise LangError("undefined variable %r" % node.name, node.span)
        if isinstance(node, YieldExpr):
            value = yield from self._eval(node.expr, frame, co)
            sent = yield value
            return sent
        if isinstance(node, Unary):
            operand = yield from self._eval(node.operand, frame, co)
            return self._eval_unary(node, operand)
        if isinstance(node, Binary):
            result = yield from self._eval_binary(node, frame, co)
            return result
        if isinstance(node, Call):
            result = yield from self._eval_call(node, frame, co)
            return result
        raise LangError("unknown expression %r" % type(node).__name__, node.span)

    def _eval_unary(self, node, operand):
        if node.op == "-":
            if isinstance(operand, bool) or not isinstance(operand, int):
                raise LangError("unary '-' requires an integer", node.span)
            return -operand
        if node.op == "not":
            return not truthy(operand)
        raise LangError("unknown unary operator %r" % node.op, node.span)

    def _eval_binary(self, node, frame, co):
        if node.op == "and":
            left = yield from self._eval(node.left, frame, co)
            if not truthy(left):
                return left
            return (yield from self._eval(node.right, frame, co))
        if node.op == "or":
            left = yield from self._eval(node.left, frame, co)
            if truthy(left):
                return left
            return (yield from self._eval(node.right, frame, co))
        left = yield from self._eval(node.left, frame, co)
        right = yield from self._eval(node.right, frame, co)
        op = node.op
        if op in ("+", "-", "*", "/", "%"):
            return self._eval_arithmetic(op, left, right, node.span)
        if op == "==":
            return _values_equal(left, right)
        if op == "!=":
            return not _values_equal(left, right)
        if op in ("<", "<=", ">", ">="):
            if not self._both_ints(left, right) and not (
                    isinstance(left, str) and isinstance(right, str)):
                raise LangError(
                    "cannot compare %s and %s"
                    % (type(left).__name__, type(right).__name__), node.span)
            if op == "<":
                return left < right
            if op == "<=":
                return left <= right
            if op == ">":
                return left > right
            return left >= right
        raise LangError("unknown operator %r" % op, node.span)

    @staticmethod
    def _both_ints(left, right):
        return (isinstance(left, int) and not isinstance(left, bool)
                and isinstance(right, int) and not isinstance(right, bool))

    def _eval_arithmetic(self, op, left, right, span):
        if op == "+" and isinstance(left, str) and isinstance(right, str):
            return left + right
        if not self._both_ints(left, right):
            raise LangError(
                "operator %r requires integers (or strings for '+')" % op, span)
        if op == "+":
            return left + right
        if op == "-":
            return left - right
        if op == "*":
            return left * right
        if right == 0:
            raise LangError("division by zero", span)
        if op == "/":
            return left // right
        return left % right

    def _eval_call(self, node, frame, co):
        if not isinstance(node.func, Var):
            raise LangError("callee must be a function name", node.span)
        name = node.func.name
        args = []
        for arg_expr in node.args:
            args.append((yield from self._eval(arg_expr, frame, co)))
        if frame.has(name):
            raise LangError("%r is not callable" % name, node.span)
        builtin = self.builtins.get(name)
        if builtin is not None:
            return builtin(args, self)
        fn_def = self.functions.get(name)
        if fn_def is None:
            raise LangError("undefined function %r" % name, node.span)
        if len(args) != len(fn_def.params):
            raise LangError(
                "%s expects %d argument(s), got %d"
                % (name, len(fn_def.params), len(args)), node.span)
        result = yield from self._run_function(fn_def, args, co)
        return result
