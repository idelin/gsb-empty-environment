"""Tree-walking interpreter (no eval, no external compiler)."""

from __future__ import annotations

from dataclasses import dataclass

from .ast_nodes import (
    App, BinOp, BoolLit, Extend, FieldAccess, Fn, If, IntLit, Let, RecordLit,
    UnaryOp, Update, Var,
)
from .errors import LangError


@dataclass
class Closure:
    params: list
    body: object
    env: dict


def eval_node(node, env):
    if isinstance(node, IntLit):
        return node.value
    if isinstance(node, BoolLit):
        return node.value
    if isinstance(node, Var):
        try:
            return env[node.name]
        except KeyError:
            raise LangError(f"unbound variable '{node.name}'", node.span) from None
    if isinstance(node, RecordLit):
        return {name: eval_node(value, env) for name, value, _ in node.fields}
    if isinstance(node, FieldAccess):
        record = eval_node(node.record, env)
        try:
            return record[node.name]
        except (TypeError, KeyError):
            # Unreachable for well-typed programs.
            raise LangError(
                f"missing field '{node.name}' at run time", node.name_span
            ) from None
    if isinstance(node, Update):
        record = dict(eval_node(node.record, env))
        record[node.name] = eval_node(node.value, env)
        return record
    if isinstance(node, Extend):
        record = dict(eval_node(node.record, env))
        record[node.name] = eval_node(node.value, env)
        return record
    if isinstance(node, Let):
        value = eval_node(node.value, env)
        return eval_node(node.body, {**env, node.name: value})
    if isinstance(node, If):
        branch = node.then if eval_node(node.cond, env) else node.other
        return eval_node(branch, env)
    if isinstance(node, UnaryOp):
        return -eval_node(node.operand, env)
    if isinstance(node, BinOp):
        left = eval_node(node.left, env)
        right = eval_node(node.right, env)
        if node.op == "+":
            return left + right
        if node.op == "-":
            return left - right
        if node.op == "*":
            return left * right
        if node.op == "/":
            if right == 0:
                raise LangError("division by zero", node.span)
            return left // right
        if node.op == "==":
            return left == right
        if node.op == "!=":
            return left != right
        if node.op == "<":
            return left < right
        if node.op == "<=":
            return left <= right
        if node.op == ">":
            return left > right
        if node.op == ">=":
            return left >= right
        raise AssertionError(f"unknown operator {node.op}")
    if isinstance(node, Fn):
        return Closure(node.params, node.body, dict(env))
    if isinstance(node, App):
        func = eval_node(node.func, env)
        if not isinstance(func, Closure):
            raise LangError("attempted to call a non-function", node.func.span)
        args = [eval_node(arg, env) for arg in node.args]
        inner = dict(func.env)
        for param, arg in zip(func.params, args):
            inner[param.name] = arg
        return eval_node(func.body, inner)
    raise AssertionError(f"unknown node {node!r}")


def format_value(value) -> str:
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, dict):
        inner = ", ".join(f"{name} = {format_value(v)}" for name, v in value.items())
        return "{" + inner + "}"
    if isinstance(value, Closure):
        return "<fn>"
    raise AssertionError(f"unknown value {value!r}")
