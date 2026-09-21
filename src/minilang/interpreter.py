from __future__ import annotations

from dataclasses import dataclass

from .common import RuntimeErr
from .nodes import App, BinOp, BoolLit, If, IntLit, Lam, Let, Var


@dataclass(frozen=True)
class Closure:
    param: str
    body: object
    env: dict


def _truthy(value) -> bool:
    if isinstance(value, bool):
        return value
    raise RuntimeErr("condition must evaluate to a Boolean")


class Interpreter:
    def eval(self, node, env: dict):
        if isinstance(node, IntLit):
            return node.value
        if isinstance(node, BoolLit):
            return node.value
        if isinstance(node, Var):
            if node.name not in env:
                raise RuntimeErr(f"unbound variable {node.name!r}")
            return env[node.name]
        if isinstance(node, Lam):
            return Closure(node.param, node.body, dict(env))
        if isinstance(node, App):
            fn = self.eval(node.fn, env)
            arg = self.eval(node.arg, env)
            if not isinstance(fn, Closure):
                raise RuntimeErr("only functions can be applied")
            call_env = dict(fn.env)
            call_env[fn.param] = arg
            return self.eval(fn.body, call_env)
        if isinstance(node, Let):
            value = self.eval(node.rhs, env)
            new_env = dict(env)
            new_env[node.name] = value
            return self.eval(node.body, new_env)
        if isinstance(node, BinOp):
            left = self.eval(node.left, env)
            right = self.eval(node.right, env)
            return self._binop(node.op, left, right)
        if isinstance(node, If):
            return self.eval(node.then if _truthy(self.eval(node.cond, env)) else node.otherwise, env)
        raise AssertionError(f"unhandled node {node!r}")

    def _binop(self, op: str, left, right):
        if op == "+":
            return left + right
        if op == "-":
            return left - right
        if op == "*":
            return left * right
        if op == "/":
            if right == 0:
                raise RuntimeErr("division by zero")
            q = abs(left) // abs(right)
            return q if (left < 0) == (right < 0) else -q
        if op == "<":
            return left < right
        if op == ">":
            return left > right
        if op == "<=":
            return left <= right
        if op == ">=":
            return left >= right
        if op == "==":
            return left is right if isinstance(left, Closure) else left == right
        if op == "!=":
            return not self._binop("==", left, right)
        raise RuntimeErr(f"unknown operator {op!r}")


def format_value(value) -> str:
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, Closure):
        return f"<function {value.param}>"
    return str(value)
