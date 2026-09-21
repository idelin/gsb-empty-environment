"""树遍历解释器:直接执行展开后的 AST,不使用 eval/compile。"""
from __future__ import annotations

import operator

from .ast_nodes import Call, Node, Number, Symbol, Text, render
from .errors import EvalError


class Env:
    __slots__ = ("vars", "parent")

    def __init__(self, parent: "Env | None" = None):
        self.vars: dict[str, object] = {}
        self.parent = parent

    def get(self, name: str):
        env = self
        while env is not None:
            if name in env.vars:
                return env.vars[name]
            env = env.parent
        raise KeyError(name)

    def set(self, name: str, value) -> None:
        env = self
        while env is not None:
            if name in env.vars:
                env.vars[name] = value
                return
            env = env.parent
        self.vars[name] = value

    def define(self, name: str, value) -> None:
        self.vars[name] = value


def format_value(value) -> str:
    if value is None:
        return "nil"
    if value is True:
        return "true"
    if value is False:
        return "false"
    return str(value)


def _plus(*args):
    result = 0
    for x in args:
        result = result + x
    return result


def _minus(*args):
    if not args:
        raise EvalError("'-' needs at least 1 argument")
    if len(args) == 1:
        return -args[0]
    result = args[0]
    for x in args[1:]:
        result -= x
    return result


def _mul(*args):
    result = 1
    for x in args:
        result = result * x
    return result


def _div(*args):
    if len(args) < 2:
        raise EvalError("'/' needs at least 2 arguments")
    result = args[0]
    for x in args[1:]:
        if isinstance(result, int) and isinstance(x, int):
            result = result // x
        else:
            result = result / x
    return result


def _cmp(op):
    def check(*args):
        return all(op(a, b) for a, b in zip(args, args[1:]))

    return check


def make_global_env(emit=print) -> Env:
    def _print(*args):
        emit(" ".join(format_value(a) for a in args))
        return None

    env = Env()
    env.vars.update(
        {
            "true": True,
            "false": False,
            "nil": None,
            "+": _plus,
            "-": _minus,
            "*": _mul,
            "/": _div,
            "%": lambda a, b: a % b,
            "=": _cmp(operator.eq),
            "!=": _cmp(operator.ne),
            "<": _cmp(operator.lt),
            "<=": _cmp(operator.le),
            ">": _cmp(operator.gt),
            ">=": _cmp(operator.ge),
            "not": lambda x: not x,
            "print": _print,
        }
    )
    return env


def _eval_seq(items, env):
    result = None
    for item in items:
        result = eval_node(item, env)
    return result


def _eval_let(node: Call, env: Env):
    items = node.items
    if len(items) < 3 or not isinstance(items[1], Call):
        raise EvalError("let expects (let ((name init)...) body...)", node.span)
    local = Env(parent=env)
    for binding in items[1].items:
        if (
            not isinstance(binding, Call)
            or len(binding.items) != 2
            or not isinstance(binding.items[0], Symbol)
        ):
            raise EvalError("malformed let binding", binding.span)
        local.define(binding.items[0].name, eval_node(binding.items[1], local))
    return _eval_seq(items[2:], local)


def eval_node(node: Node, env: Env):
    if isinstance(node, Number):
        return node.value
    if isinstance(node, Text):
        return node.value
    if isinstance(node, Symbol):
        try:
            return env.get(node.name)
        except KeyError:
            raise EvalError(f"unbound variable '{node.name}'", node.span) from None
    if isinstance(node, Call):
        if not node.items:
            raise EvalError("cannot evaluate empty form '()'", node.span)
        head = node.items[0]
        if isinstance(head, Symbol):
            name = head.name
            if name == "do":
                return _eval_seq(node.items[1:], env)
            if name == "let":
                return _eval_let(node, env)
            if name == "set":
                if len(node.items) != 3 or not isinstance(node.items[1], Symbol):
                    raise EvalError("set expects (set name expr)", node.span)
                value = eval_node(node.items[2], env)
                env.set(node.items[1].name, value)
                return value
            if name == "if":
                if len(node.items) not in (3, 4):
                    raise EvalError("if expects (if cond then [else])", node.span)
                if eval_node(node.items[1], env):
                    return eval_node(node.items[2], env)
                if len(node.items) == 4:
                    return eval_node(node.items[3], env)
                return None
            if name == "while":
                if len(node.items) < 2:
                    raise EvalError("while expects (while cond body...)", node.span)
                result = None
                while eval_node(node.items[1], env):
                    result = _eval_seq(node.items[2:], env)
                return result
            if name == "and":
                result = True
                for item in node.items[1:]:
                    result = eval_node(item, env)
                    if not result:
                        return result
                return result
            if name == "or":
                for item in node.items[1:]:
                    result = eval_node(item, env)
                    if result:
                        return result
                return False
            if name == "defmacro":
                raise EvalError("defmacro is only allowed at top level", node.span)
        fn = eval_node(head, env)
        if not callable(fn):
            raise EvalError(
                f"head of call is not a function: {render(head)}", head.span
            )
        args = [eval_node(a, env) for a in node.items[1:]]
        return fn(*args)
    raise EvalError("cannot evaluate node", node.span)
