"""Independent tree-walking interpreter for the *original* expression.

This is the reference semantics: every simplification produced by the engine
is cross-checked against it.  Evaluation is strict, left-to-right, depth
first; the first failure encountered is reported with the source position
(``origin``) of the responsible node.  No host ``eval`` is ever used.
"""

from __future__ import annotations

from fractions import Fraction


class UndefinedError(Exception):
    """The expression is undefined at the given source position."""

    def __init__(self, pos: int, reason: str = "undefined"):
        super().__init__(f"undefined at position {pos}: {reason}")
        self.pos = pos
        self.reason = reason


class UnboundError(Exception):
    """A variable has no binding (only raised for incomplete environments)."""

    def __init__(self, name: str):
        super().__init__(f"unbound variable: {name}")
        self.name = name


def evaluate(n, env: dict[str, Fraction]) -> Fraction:
    op = n.op
    if op == "const":
        return n.value
    if op == "undef":
        raise UndefinedError(n.origin, "empty domain")
    if op == "var":
        try:
            return env[n.name]
        except KeyError:
            raise UnboundError(n.name) from None
    if op == "neg":
        return -evaluate(n.args[0], env)
    if op in ("add", "sub", "mul", "div"):
        left = evaluate(n.args[0], env)     # left operand first
        right = evaluate(n.args[1], env)
        if op == "add":
            return left + right
        if op == "sub":
            return left - right
        if op == "mul":
            return left * right
        if right == 0:
            raise UndefinedError(n.origin, "division by zero")
        return left / right
    if op == "cond":
        c = evaluate(n.args[0], env)        # condition first
        return evaluate(n.args[1] if c != 0 else n.args[2], env)
    raise ValueError(f"unknown op: {op}")   # pragma: no cover


def run(n, env) -> tuple:
    """Total wrapper: ('ok', Fraction) or ('undef', position)."""
    try:
        return ("ok", evaluate(n, env))
    except UndefinedError as u:
        return ("undef", u.pos)
