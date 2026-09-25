"""Tree-walking interpreter for the declaration language."""
from __future__ import annotations

from .ast import BinOp, Num, Paren, Str, Var
from .diagnostics import Diagnostic
from .tokens import MINUS, PLUS, SLASH, STAR

_NUMBER = (int, float)


def _eval(node, env, diagnostics):
    if isinstance(node, Num):
        return node.value
    if isinstance(node, Str):
        return node.value
    if isinstance(node, Var):
        if node.name in env:
            return env[node.name]
        if node.token.virtual:
            # Fabricated by recovery; the parse diagnostic already covers
            # it, so stay neutral instead of cascading runtime noise.
            return 0
        diagnostics.append(Diagnostic(f"undefined variable {node.name!r}",
                                      node.token.start, node.token.end,
                                      source="runtime"))
        return 0
    if isinstance(node, Paren):
        return _eval(node.expr, env, diagnostics)
    if isinstance(node, BinOp):
        left = _eval(node.left, env, diagnostics)
        right = _eval(node.right, env, diagnostics)
        if node.op == PLUS and isinstance(left, str) and isinstance(right, str):
            return left + right
        if isinstance(left, _NUMBER) and isinstance(right, _NUMBER):
            if node.op == PLUS:
                return left + right
            if node.op == MINUS:
                return left - right
            if node.op == STAR:
                return left * right
            if node.op == SLASH:
                if right == 0:
                    diagnostics.append(Diagnostic(
                        "division by zero", node.token.start, node.token.end,
                        source="runtime"))
                    return 0
                return left / right
        diagnostics.append(Diagnostic(
            f"type error: cannot apply {node.op} to "
            f"{type(left).__name__} and {type(right).__name__}",
            node.token.start, node.token.end, source="runtime"))
        return 0
    raise TypeError(f"unknown node {node!r}")


def execute(program, env=None):
    env = {} if env is None else env
    diagnostics = []
    for decl in program.decls:
        env[decl.name] = _eval(decl.expr, env, diagnostics)
    return env, diagnostics
