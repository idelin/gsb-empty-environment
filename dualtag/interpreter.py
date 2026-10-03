"""A tiny pure-expression interpreter (stdlib ast only).

Supports numeric constants, names from an environment, + - * / **,
unary +/-, calls to sin cos tan exp log sqrt abs min max, comparisons,
and Python-style conditional expressions (a if cond else b).

Non-smooth constructs are first-class: abs/min/max at a kink and a
branch condition sitting exactly on its boundary raise
NonDifferentiableError instead of silently picking a subgradient.
"""
from __future__ import annotations

import ast

from . import budget as _budget
from . import dual as D
from .errors import InterpreterError, NonDifferentiableError

_FUNCS = {
    "sin": D.sin, "cos": D.cos, "tan": D.tan, "exp": D.exp,
    "log": D.log, "sqrt": D.sqrt, "abs": D.dual_abs,
    "min": D.dual_min, "max": D.dual_max,
}

_BINOPS = {
    ast.Add: D.add, ast.Sub: D.sub, ast.Mult: D.mul,
    ast.Div: D.truediv, ast.Pow: D.pow_,
}


class Interpreter:
    def __init__(self, env=None):
        self.env = dict(env or {})

    def eval(self, source):
        try:
            tree = ast.parse(source, mode="eval")
        except SyntaxError as exc:
            raise InterpreterError(f"syntax error: {exc}") from exc
        return self._eval(tree.body)

    def _eval(self, node):
        _budget.tick()
        if isinstance(node, ast.Constant):
            if isinstance(node.value, bool) or not isinstance(node.value, (int, float)):
                raise InterpreterError(f"unsupported constant {node.value!r}")
            return float(node.value)
        if isinstance(node, ast.Name):
            if node.id in self.env:
                return self.env[node.id]
            raise InterpreterError(f"unknown name {node.id!r}")
        if isinstance(node, ast.BinOp):
            op = _BINOPS.get(type(node.op))
            if op is None:
                raise InterpreterError(f"unsupported operator {type(node.op).__name__}")
            return op(self._eval(node.left), self._eval(node.right))
        if isinstance(node, ast.UnaryOp):
            if isinstance(node.op, ast.USub):
                return D.neg(self._eval(node.operand))
            if isinstance(node.op, ast.UAdd):
                return self._eval(node.operand)
            raise InterpreterError(f"unsupported unary operator {type(node.op).__name__}")
        if isinstance(node, ast.Call):
            if not isinstance(node.func, ast.Name) or node.func.id not in _FUNCS:
                raise InterpreterError(
                    "only calls to: " + ", ".join(sorted(_FUNCS)))
            if node.keywords:
                raise InterpreterError("keyword arguments are not supported")
            args = [self._eval(a) for a in node.args]
            return _FUNCS[node.func.id](*args)
        if isinstance(node, ast.IfExp):
            return self._eval_ifexp(node)
        if isinstance(node, ast.Compare):
            value, _, _ = self._eval_compare(node)
            return value
        raise InterpreterError(f"unsupported syntax: {type(node).__name__}")

    def _eval_compare(self, node):
        if len(node.ops) != 1:
            raise InterpreterError("chained comparisons are not supported")
        left = self._eval(node.left)
        right = self._eval(node.comparators[0])
        a, b = D.deep_primal(left), D.deep_primal(right)
        has_dual = bool(D.partials_of(left) or D.partials_of(right))
        boundary = a == b
        op = node.ops[0]
        if isinstance(op, ast.Lt):
            value = a < b
        elif isinstance(op, ast.LtE):
            value = a <= b
        elif isinstance(op, ast.Gt):
            value = a > b
        elif isinstance(op, ast.GtE):
            value = a >= b
        elif isinstance(op, ast.Eq):
            value = a == b
        elif isinstance(op, ast.NotEq):
            value = a != b
        else:
            raise InterpreterError(f"unsupported comparison {type(op).__name__}")
        return value, boundary, has_dual

    def _eval_ifexp(self, node):
        if isinstance(node.test, ast.Compare):
            value, boundary, has_dual = self._eval_compare(node.test)
            if boundary and has_dual:
                raise NonDifferentiableError(
                    "branch condition sits exactly on its boundary; "
                    "the result is non-differentiable there")
        else:
            value = bool(D.deep_primal(self._eval(node.test)))
        return self._eval(node.body if value else node.orelse)


def eval_expr(source, **env):
    return Interpreter(env).eval(source)


def compile_expression(source, params):
    """Compile source into a Python callable f(*args) over named params."""
    params = tuple(params)

    def f(*args):
        if len(args) != len(params):
            raise TypeError(f"expected {len(params)} args, got {len(args)}")
        return Interpreter(dict(zip(params, args))).eval(source)

    f.source = source
    f.params = params
    return f
