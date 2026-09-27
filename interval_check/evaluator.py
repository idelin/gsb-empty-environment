"""Exact integer evaluation of a parsed expression."""

from __future__ import annotations

from .parser import Node


class DivisionByZero(Exception):
    """Raised when a concrete evaluation hits divisor zero."""


def evaluate(node: Node, env: dict[str, int]) -> int:
    op = node.op
    if op == "const":
        return int(node.value)
    if op == "var":
        return env[str(node.value)]
    if op == "neg":
        return -evaluate(node.left, env)  # type: ignore[arg-type]
    left = evaluate(node.left, env)  # type: ignore[arg-type]
    right = evaluate(node.right, env)  # type: ignore[arg-type]
    if op == "+":
        return left + right
    if op == "-":
        return left - right
    if op == "*":
        return left * right
    if op == "//":
        if right == 0:
            raise DivisionByZero("floor division by zero")
        return left // right
    raise ValueError(f"unknown operator: {op!r}")
