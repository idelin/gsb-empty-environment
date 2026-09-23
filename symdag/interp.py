"""Independent reference interpreter for expression DAGs.

This is the ground-truth semantics used by the differential tests: it
walks the DAG directly (children left to right), computes exact rational
values with fractions.Fraction, and raises UndefinedError carrying the
*origin path* of the failing node so error positions can be compared
between the original and the simplified expression.  It never calls the
host eval/exec.
"""
from __future__ import annotations

from fractions import Fraction


class UndefinedError(Exception):
    """Raised when the expression is undefined at the given source path."""

    def __init__(self, path, reason):
        self.path = tuple(path)
        self.reason = reason
        super().__init__(f"undefined at {self.path}: {reason}")


def _undefined(node, reason):
    raise UndefinedError(node.origin, reason)


def interpret(node, env) -> Fraction:
    """Evaluate ``node`` under ``env`` (name -> Fraction).  Exact arithmetic."""
    op = node.op
    if op == "const":
        return node.value
    if op == "undef":
        _undefined(node, node.reason or "undefined")
    if op == "var":
        try:
            return env[node.name]
        except KeyError:
            _undefined(node, f"unbound variable {node.name!r}")
    if op == "neg":
        return -interpret(node.args[0], env)
    if op == "if":
        cond = interpret(node.args[0], env)
        return interpret(node.args[1] if cond != 0 else node.args[2], env)
    left = interpret(node.args[0], env)
    right = interpret(node.args[1], env)
    if op == "add":
        return left + right
    if op == "sub":
        return left - right
    if op == "mul":
        return left * right
    if op == "div":
        if right == 0:
            _undefined(node, "division by zero")
        return left / right
    if op == "lt":
        return Fraction(1 if left < right else 0)
    if op == "eq":
        return Fraction(1 if left == right else 0)
    raise ValueError(f"unknown op {op!r}")
