"""树遍历解释器：直接执行展开后的语法树，不借助 eval/compile。"""
from __future__ import annotations

import sys

from .errors import EvalError
from .nodes import Form, IntLit, Node, StrLit, Sym


class Env:
    """词法作用域链。"""

    __slots__ = ("vars", "parent")

    def __init__(self, parent: "Env | None" = None):
        self.vars: dict[str, object] = {}
        self.parent = parent

    def define(self, name: str, value: object) -> None:
        self.vars[name] = value

    def lookup(self, name: str, span=None):
        env: Env | None = self
        while env is not None:
            if name in env.vars:
                return env.vars[name]
            env = env.parent
        raise EvalError(f"unbound variable {name!r}", span)

    def assign(self, name: str, value: object, span=None) -> None:
        env: Env | None = self
        while env is not None:
            if name in env.vars:
                env.vars[name] = value
                return
            env = env.parent
        raise EvalError(f"cannot set unbound variable {name!r}", span)


def _truthy(value: object) -> bool:
    return bool(value)


class Interpreter:
    def __init__(self, out=None):
        self.out = out if out is not None else sys.stdout

    def run(self, forms: list[Node]):
        env = Env()
        result = None
        for form in forms:
            result = self.eval(form, env)
        return result

    def eval(self, node: Node, env: Env):
        if isinstance(node, IntLit):
            return node.value
        if isinstance(node, StrLit):
            return node.value
        if isinstance(node, Sym):
            return env.lookup(node.name, node.span)
        if isinstance(node, Form):
            return self._eval_form(node, env)
        raise EvalError(f"cannot evaluate node of type {type(node).__name__}")

    def _eval_form(self, node: Form, env: Env):
        items = node.items
        if not items:
            return None
        head = items[0]
        if not isinstance(head, Sym):
            raise EvalError("form head must be a symbol", node.span)
        name = head.name
        args = items[1:]

        if name == "seq":
            result = None
            for arg in args:
                result = self.eval(arg, env)
            return result
        if name == "def":
            self._expect(node, 2)
            target = self._expect_sym(args[0], "def")
            value = self.eval(args[1], env)
            env.define(target, value)
            return value
        if name == "set":
            self._expect(node, 2)
            target = self._expect_sym(args[0], "set")
            value = self.eval(args[1], env)
            env.assign(target, value, node.span)
            return value
        if name == "let":
            self._expect(node, 3)
            target = self._expect_sym(args[0], "let")
            value = self.eval(args[1], env)
            child = Env(env)
            child.define(target, value)
            return self.eval(args[2], child)
        if name == "if":
            self._expect(node, 3)
            cond = self.eval(args[0], env)
            return self.eval(args[1] if _truthy(cond) else args[2], env)
        if name == "while":
            self._expect(node, 2)
            result = None
            while _truthy(self.eval(args[0], env)):
                result = self.eval(args[1], env)
            return result
        if name == "print":
            values = [self.eval(arg, env) for arg in args]
            print(*values, file=self.out)
            return values[-1] if values else None
        if name in ("+", "-", "*", "/", "%"):
            return self._arith(name, args, env, node)
        if name in ("<", "<=", ">", ">=", "="):
            self._expect(node, 2)
            left = self.eval(args[0], env)
            right = self.eval(args[1], env)
            return self._compare(name, left, right)
        if name == "not":
            self._expect(node, 1)
            return not _truthy(self.eval(args[0], env))
        raise EvalError(f"unknown form or builtin {name!r}", head.span)

    def _expect(self, node: Form, argc: int) -> None:
        if len(node.items) - 1 != argc:
            head = node.items[0]
            label = head.name if isinstance(head, Sym) else "form"
            raise EvalError(
                f"{label!r} expects {argc} argument(s), "
                f"got {len(node.items) - 1}",
                node.span,
            )

    def _expect_sym(self, node: Node, context: str) -> str:
        if not isinstance(node, Sym):
            raise EvalError(f"{context} target must be a symbol", node.span)
        return node.name

    def _arith(self, op: str, args: list[Node], env: Env, node: Form):
        values = [self.eval(arg, env) for arg in args]
        if not values:
            raise EvalError(f"{op!r} needs at least one argument", node.span)
        if op == "+":
            result = 0
            for v in values:
                result += v
            return result
        if op == "*":
            result = 1
            for v in values:
                result *= v
            return result
        if op == "-":
            if len(values) == 1:
                return -values[0]
            result = values[0]
            for v in values[1:]:
                result -= v
            return result
        self._expect(node, 2)
        left, right = values
        if op == "/":
            if right == 0:
                raise EvalError("division by zero", node.span)
            return left // right
        return left % right

    def _compare(self, op: str, left, right) -> bool:
        if op == "<":
            return left < right
        if op == "<=":
            return left <= right
        if op == ">":
            return left > right
        if op == ">=":
            return left >= right
        return left == right
