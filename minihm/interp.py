"""Tree-walking interpreter (implemented from scratch, no eval/exec)."""
from __future__ import annotations

from dataclasses import dataclass

from .astnodes import App, BinOp, BoolLit, If, IntLit, Lam, Let, Node, Var
from .errors import RuntimeFailure


@dataclass
class Closure:
    param: str
    body: Node
    env: dict


def evaluate(node: Node, env: dict):
    if isinstance(node, IntLit):
        return node.value
    if isinstance(node, BoolLit):
        return node.value
    if isinstance(node, Var):
        if node.name not in env:
            raise RuntimeFailure(f"unbound variable `{node.name}`", node.span)
        return env[node.name]
    if isinstance(node, Lam):
        return Closure(node.param, node.body, dict(env))
    if isinstance(node, App):
        fn = evaluate(node.fn, env)
        arg = evaluate(node.arg, env)
        if not isinstance(fn, Closure):
            raise RuntimeFailure("attempted to call a non-function", node.span)
        new_env = dict(fn.env)
        new_env[fn.param] = arg
        return evaluate(fn.body, new_env)
    if isinstance(node, Let):
        value = evaluate(node.value, env)
        new_env = dict(env)
        new_env[node.name] = value
        return evaluate(node.body, new_env)
    if isinstance(node, If):
        cond = evaluate(node.cond, env)
        if not isinstance(cond, bool):
            raise RuntimeFailure("`if` condition is not a boolean", node.cond.span)
        return evaluate(node.then if cond else node.otherwise, env)
    if isinstance(node, BinOp):
        left = evaluate(node.left, env)
        right = evaluate(node.right, env)
        return _apply_binop(node, left, right)
    raise AssertionError(f"unknown node {node!r}")


def _apply_binop(node: BinOp, left, right):
    op = node.op
    if op == "==":
        return left == right
    for value in (left, right):
        if isinstance(value, bool) or not isinstance(value, int):
            raise RuntimeFailure(f"`{op}` expects integer operands", node.op_span)
    if op == "+":
        return left + right
    if op == "-":
        return left - right
    if op == "*":
        return left * right
    if op == "<":
        return left < right
    raise AssertionError(f"unknown operator {op}")


def show_value(value) -> str:
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, Closure):
        return "<function>"
    return repr(value)
