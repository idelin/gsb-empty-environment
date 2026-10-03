"""A tiny symbolic differentiator over the same expression language.

Used as an independent cross-check for the dual-number engine: it
produces a new expression string that the Interpreter can evaluate.
Non-smooth constructs raise NonDifferentiableError.
"""
from __future__ import annotations

import ast

from .errors import InterpreterError, NonDifferentiableError

__all__ = ["symbolic_derivative"]


def _const(v):
    return ast.Constant(value=float(v))


def _is_const(node, value=None):
    return isinstance(node, ast.Constant) and (value is None or node.value == value)


def _add(a, b):
    if _is_const(a, 0.0):
        return b
    if _is_const(b, 0.0):
        return a
    if _is_const(a) and _is_const(b):
        return _const(a.value + b.value)
    return ast.BinOp(left=a, op=ast.Add(), right=b)


def _sub(a, b):
    if _is_const(b, 0.0):
        return a
    if _is_const(a) and _is_const(b):
        return _const(a.value - b.value)
    return ast.BinOp(left=a, op=ast.Sub(), right=b)


def _neg(a):
    if _is_const(a):
        return _const(-a.value)
    return ast.UnaryOp(op=ast.USub(), operand=a)


def _mul(a, b):
    if _is_const(a, 0.0) or _is_const(b, 0.0):
        return _const(0.0)
    if _is_const(a, 1.0):
        return b
    if _is_const(b, 1.0):
        return a
    if _is_const(a) and _is_const(b):
        return _const(a.value * b.value)
    return ast.BinOp(left=a, op=ast.Mult(), right=b)


def _div(a, b):
    if _is_const(a, 0.0):
        return _const(0.0)
    return ast.BinOp(left=a, op=ast.Div(), right=b)


def _pow_const(base, c):
    if c == 0.0:
        return _const(1.0)
    if c == 1.0:
        return base
    return ast.BinOp(left=base, op=ast.Pow(), right=_const(c))


def _call(name, arg):
    return ast.Call(func=ast.Name(id=name, ctx=ast.Load()),
                    args=[arg], keywords=[])


def _diff(node, var):
    if isinstance(node, ast.Constant):
        return _const(0.0)
    if isinstance(node, ast.Name):
        return _const(1.0 if node.id == var else 0.0)
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.USub):
        return _neg(_diff(node.operand, var))
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.UAdd):
        return _diff(node.operand, var)
    if isinstance(node, ast.BinOp):
        left, right = node.left, node.right
        dl, dr = _diff(left, var), _diff(right, var)
        if isinstance(node.op, ast.Add):
            return _add(dl, dr)
        if isinstance(node.op, ast.Sub):
            return _sub(dl, dr)
        if isinstance(node.op, ast.Mult):
            return _add(_mul(dl, right), _mul(left, dr))
        if isinstance(node.op, ast.Div):
            return _div(_sub(_mul(dl, right), _mul(left, dr)),
                        _pow_const(right, 2.0))
        if isinstance(node.op, ast.Pow):
            if _is_const(right):
                c = right.value
                return _mul(_mul(_const(c), _pow_const(left, c - 1.0)), dl)
            inner = _add(_mul(dr, _call("log", left)),
                         _div(_mul(right, dl), left))
            return _mul(ast.BinOp(left=left, op=ast.Pow(), right=right), inner)
    if isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
        name = node.func.id
        if name in ("abs", "min", "max"):
            raise NonDifferentiableError(
                f"{name} is non-smooth; no symbolic derivative")
        if len(node.args) != 1:
            raise InterpreterError(f"{name} takes exactly one argument here")
        u = node.args[0]
        du = _diff(u, var)
        if name == "sin":
            return _mul(_call("cos", u), du)
        if name == "cos":
            return _neg(_mul(_call("sin", u), du))
        if name == "tan":
            return _div(du, _pow_const(_call("cos", u), 2.0))
        if name == "exp":
            return _mul(_call("exp", u), du)
        if name == "log":
            return _div(du, u)
        if name == "sqrt":
            return _div(du, _mul(_const(2.0), _call("sqrt", u)))
    if isinstance(node, ast.IfExp):
        raise NonDifferentiableError(
            "branch expressions are non-smooth; no symbolic derivative")
    raise InterpreterError(f"cannot symbolically differentiate {type(node).__name__}")


def symbolic_derivative(source, var):
    """Return a new expression string for d(source)/d(var)."""
    try:
        tree = ast.parse(source, mode="eval")
    except SyntaxError as exc:
        raise InterpreterError(f"syntax error: {exc}") from exc
    derived = _diff(tree.body, var)
    return ast.unparse(ast.Expression(body=derived))
