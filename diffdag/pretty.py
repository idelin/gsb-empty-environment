"""中缀格式化输出（带优先级括号）。"""
from __future__ import annotations

from .expr import Node

_PRECEDENCE = {"add": 1, "mul": 2, "pow": 3}


def to_str(node: Node) -> str:
    return _fmt(node, 0)


def _fmt_const(node: Node) -> str:
    v = node.value
    if v.denominator == 1:
        return str(v.numerator) if v >= 0 else f"({v.numerator})"
    return f"({v})"


def _fmt(node: Node, parent_prec: int) -> str:
    op = node.op
    if op == "const":
        return _fmt_const(node)
    if op == "var":
        return node.name
    if op in ("sin", "cos", "exp", "log"):
        return f"{op}({_fmt(node.args[0], 0)})"
    if op == "add":
        s = f"{_fmt(node.args[0], 1)} + {_fmt(node.args[1], 1)}"
    elif op == "mul":
        s = f"{_fmt(node.args[0], 2)}*{_fmt(node.args[1], 2)}"
    elif op == "pow":
        base = _fmt(node.args[0], 3)
        n = node.value
        if n.denominator == 1 and n >= 0:
            s = f"{base}^{n.numerator}"
        else:
            s = f"{base}^({n})"
    else:
        raise ValueError(f"未知算子: {op}")
    return f"({s})" if _PRECEDENCE[op] < parent_prec else s
