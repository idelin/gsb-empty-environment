"""Tree-walking interpreter with a module loader.

Module semantics: modules are initialized lazily on first import and cached,
so a module's top-level side effects run exactly once. Function declarations
are hoisted before any top-level code runs, so functions may mutually
reference each other, even across an import cycle. `const` bindings start
uninitialized; reading one before its initializer finishes is an error, and
for an import cycle the error reports the initialization chain (for example
"a -> b -> a"). `import { x } from "m"` creates a live alias: reads go
through to the exporting module's binding, so linking works as long as the
value is initialized by the time it is actually read.
"""

from __future__ import annotations

from .ast_nodes import (
    Bin, Bool, Call, ConstDecl, ExprStmt, FnDecl, IfStmt, Import, Name,
    Num, ReturnStmt, Str, Unary,
)
from .lexer import LexError, tokenize
from .parser import ParseError, Parser

UNINIT, INITING, READY = 0, 1, 2


class LangError(Exception):
    def __init__(self, message, span=None, module=None, chain=None):
        super().__init__(message)
        self.message = message
        self.span = span
        self.module = module
        self.chain = chain

    def __str__(self):
        parts = [self.message]
        if self.module is not None:
            parts.append(f"in module '{self.module}'")
        if self.span is not None:
            parts.append(f"at bytes {self.span[0]}..{self.span[1]}")
        if self.chain:
            parts.append("initialization chain: " + " -> ".join(self.chain))
        return " ".join(parts)


class Binding:
    __slots__ = ("state", "value")

    def __init__(self, state=UNINIT, value=None):
        self.state = state
        self.value = value


class Alias:
    """An import binding: a live reference to another module's binding."""

    __slots__ = ("record", "name")

    def __init__(self, record, name):
        self.record = record
        self.name = name


class Env:
    __slots__ = ("vars", "parent")

    def __init__(self, parent=None):
        self.vars = {}
        self.parent = parent

    def define(self, name, slot):
        self.vars[name] = slot

    def lookup_slot(self, name):
        env = self
        while env is not None:
            if name in env.vars:
                return env.vars[name]
            env = env.parent
        return None


class FnValue:
    __slots__ = ("decl", "def_env", "module_name")

    def __init__(self, decl, def_env, module_name):
        self.decl = decl
        self.def_env = def_env
        self.module_name = module_name


class BuiltinFn:
    __slots__ = ("name", "fn")

    def __init__(self, name, fn):
        self.name = name
        self.fn = fn


class ReturnSignal(Exception):
    def __init__(self, value):
        self.value = value


class ModuleRecord:
    def __init__(self, name, source, ast, env):
        self.name = name
        self.source = source
        self.ast = ast
        self.env = env
        self.exports = {
            item.name
            for item in ast.items
            if isinstance(item, (ConstDecl, FnDecl)) and item.exported
        }
        self.state = "initializing"


class Interpreter:
    def __init__(self, sources, output=None):
        self.sources = dict(sources)
        self.modules = {}
        self.init_stack = []
        self.output = output if output is not None else print
        self.builtins = Env()
        self.builtins.define(
            "print", Binding(READY, BuiltinFn("print", self._builtin_print)))

    def _builtin_print(self, *args):
        self.output(" ".join(self._display(a) for a in args))

    @staticmethod
    def _display(value):
        if value is True:
            return "true"
        if value is False:
            return "false"
        if value is None:
            return "null"
        return str(value)

    def current_module(self):
        return self.init_stack[-1] if self.init_stack else None

    def load(self, name, span=None):
        record = self.modules.get(name)
        if record is not None:
            return record
        if name not in self.sources:
            raise LangError(f"module '{name}' not found", span,
                            module=self.current_module())
        source = self.sources[name]
        try:
            tokens = tokenize(source)
            ast = Parser(tokens).parse_module()
        except LexError as exc:
            raise LangError(exc.message, (exc.start, exc.end), module=name) from None
        except ParseError as exc:
            raise LangError(exc.message, (exc.start, exc.end), module=name) from None
        record = ModuleRecord(name, source, ast, Env(parent=self.builtins))
        self.modules[name] = record
        self.init_stack.append(name)
        try:
            for item in ast.items:
                if isinstance(item, FnDecl):
                    record.env.define(
                        item.name,
                        Binding(READY, FnValue(item, record.env, name)))
                elif isinstance(item, ConstDecl):
                    record.env.define(item.name, Binding())
            for item in ast.items:
                self.exec_item(item, record)
            record.state = "initialized"
        except Exception:
            del self.modules[name]
            raise
        finally:
            self.init_stack.pop()
        return record

    def exec_item(self, item, record):
        env = record.env
        if isinstance(item, Import):
            target = self.load(item.module, item.span)
            for name in item.names:
                if name not in target.exports:
                    raise LangError(
                        f"module '{item.module}' does not export '{name}'",
                        item.span, module=record.name)
                env.define(name, Alias(target, name))
        elif isinstance(item, FnDecl):
            pass
        elif isinstance(item, ConstDecl):
            self.exec_const(item, env, record.name)
        elif isinstance(item, (ExprStmt, IfStmt)):
            self.exec_stmt(item, env, record.name)
        elif isinstance(item, ReturnStmt):
            raise LangError("'return' outside of a function", item.span,
                            module=record.name)
        else:
            raise AssertionError(item)

    def exec_const(self, decl, env, mod):
        slot = env.vars.get(decl.name)
        if slot is None:
            slot = Binding()
            env.define(decl.name, slot)
        slot.state = INITING
        slot.value = self.eval(decl.expr, env, mod)
        slot.state = READY

    def exec_stmt(self, stmt, env, mod):
        if isinstance(stmt, ConstDecl):
            self.exec_const(stmt, env, mod)
        elif isinstance(stmt, ReturnStmt):
            value = self.eval(stmt.expr, env, mod) if stmt.expr is not None else None
            raise ReturnSignal(value)
        elif isinstance(stmt, IfStmt):
            if self.truthy(self.eval(stmt.cond, env, mod)):
                self.exec_block(stmt.then, env, mod)
            elif stmt.orelse is not None:
                self.exec_block(stmt.orelse, env, mod)
        elif isinstance(stmt, ExprStmt):
            self.eval(stmt.expr, env, mod)
        else:
            raise AssertionError(stmt)

    def exec_block(self, stmts, env, mod):
        for stmt in stmts:
            self.exec_stmt(stmt, env, mod)

    def read(self, env, name, span, mod):
        slot = env.lookup_slot(name)
        if slot is None:
            raise LangError(f"undefined name '{name}'", span, module=mod)
        if isinstance(slot, Alias):
            record = slot.record
            target = record.env.vars.get(slot.name)
            if target is None or target.state != READY:
                chain = list(self.init_stack)
                if not chain or chain[-1] != record.name:
                    chain.append(record.name)
                raise LangError(
                    f"imported binding '{slot.name}' from module "
                    f"'{record.name}' is not initialized yet",
                    span, module=mod, chain=chain)
            return target.value
        if slot.state == UNINIT:
            raise LangError(f"binding '{name}' is not initialized yet",
                            span, module=mod)
        if slot.state == INITING:
            raise LangError(f"circular initializer involving '{name}'",
                            span, module=mod)
        return slot.value

    def eval(self, expr, env, mod):
        if isinstance(expr, (Num, Str, Bool)):
            return expr.value
        if isinstance(expr, Name):
            return self.read(env, expr.name, expr.span, mod)
        if isinstance(expr, Unary):
            operand = self.eval(expr.operand, env, mod)
            if expr.op == "-":
                self.require_number(operand, expr.span, mod)
                return -operand
            return not self.truthy(operand)
        if isinstance(expr, Bin):
            return self.eval_bin(expr, env, mod)
        if isinstance(expr, Call):
            func = self.eval(expr.func, env, mod)
            args = [self.eval(a, env, mod) for a in expr.args]
            return self.call(func, args, expr.span, mod)
        raise AssertionError(expr)

    def eval_bin(self, expr, env, mod):
        op = expr.op
        if op == "and":
            if not self.truthy(self.eval(expr.left, env, mod)):
                return False
            return self.truthy(self.eval(expr.right, env, mod))
        if op == "or":
            if self.truthy(self.eval(expr.left, env, mod)):
                return True
            return self.truthy(self.eval(expr.right, env, mod))
        left = self.eval(expr.left, env, mod)
        right = self.eval(expr.right, env, mod)
        if op in ("+", "-", "*", "/", "%"):
            if op == "+" and isinstance(left, str) and isinstance(right, str):
                return left + right
            self.require_number(left, expr.span, mod)
            self.require_number(right, expr.span, mod)
            if op == "+":
                return left + right
            if op == "-":
                return left - right
            if op == "*":
                return left * right
            if right == 0:
                raise LangError("division by zero", expr.span, module=mod)
            if op == "/":
                return left / right
            return left % right
        if op == "==":
            return left == right
        if op == "!=":
            return left != right
        self.require_number(left, expr.span, mod)
        self.require_number(right, expr.span, mod)
        if op == "<":
            return left < right
        if op == "<=":
            return left <= right
        if op == ">":
            return left > right
        return left >= right

    def call(self, func, args, span, mod):
        if isinstance(func, BuiltinFn):
            return func.fn(*args)
        if isinstance(func, FnValue):
            params = func.decl.params
            if len(args) != len(params):
                raise LangError(
                    f"function '{func.decl.name}' expects {len(params)} "
                    f"argument(s), got {len(args)}",
                    span, module=mod)
            local = Env(parent=func.def_env)
            for pname, value in zip(params, args):
                local.define(pname, Binding(READY, value))
            try:
                self.exec_block(func.decl.body, local, func.module_name)
            except ReturnSignal as signal:
                return signal.value
            return None
        raise LangError("attempted to call a non-function value", span, module=mod)

    @staticmethod
    def truthy(value):
        if value is None or value is False:
            return False
        if isinstance(value, bool):
            return True
        if isinstance(value, (int, float)) and value == 0:
            return False
        if isinstance(value, str) and value == "":
            return False
        return True

    @staticmethod
    def require_number(value, span, mod):
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise LangError(f"expected a number, got {value!r}", span, module=mod)
