"""A tiny symbolic differentiator over :mod:`dualtag.expr` ASTs.

Exists purely as an independent cross-check for the dual-number results:
differentiate an expression symbolically, simplify, then evaluate with
plain floats.  Non-smooth nodes (abs/min/max/sign) refuse to differentiate.
"""
from __future__ import annotations

from .errors import NonDifferentiableError


def _num(v):
    return ("num", float(v))


def _bin(op, l, r):
    return ("bin", op, l, r)


def diff(ast, var):
    """Symbolic derivative of ``ast`` with respect to variable ``var``."""
    kind = ast[0]
    if kind == "num":
        return _num(0.0)
    if kind == "var":
        return _num(1.0 if ast[1] == var else 0.0)
    if kind == "neg":
        return ("neg", diff(ast[1], var))
    if kind == "bin":
        op, u, v = ast[1], ast[2], ast[3]
        du, dv = diff(u, var), diff(v, var)
        if op == "+":
            return _bin("+", du, dv)
        if op == "-":
            return _bin("-", du, dv)
        if op == "*":
            return _bin("+", _bin("*", du, v), _bin("*", u, dv))
        if op == "/":
            return _bin("/", _bin("-", _bin("*", du, v), _bin("*", u, dv)),
                        _bin("^", v, _num(2.0)))
        if op == "^":
            if v[0] == "num":
                n = v[1]
                return _bin("*", _bin("*", _num(n), _bin("^", u, _num(n - 1))), du)
            return _bin("*", _bin("^", u, v),
                        _bin("+", _bin("*", dv, ("call", "log", [u])),
                             _bin("*", v, _bin("/", du, u))))
    if kind == "call":
        name, args = ast[1], ast[2]
        if name in ("abs", "min", "max", "sign"):
            raise NonDifferentiableError(
                f"symbolic derivative of non-smooth {name!r}")
        u = args[0]
        du = diff(u, var)
        if name == "sin":
            return _bin("*", ("call", "cos", [u]), du)
        if name == "cos":
            return ("neg", _bin("*", ("call", "sin", [u]), du))
        if name == "tan":
            return _bin("/", du, _bin("^", ("call", "cos", [u]), _num(2.0)))
        if name == "exp":
            return _bin("*", ("call", "exp", [u]), du)
        if name == "log":
            return _bin("/", du, u)
        if name == "sqrt":
            return _bin("/", du, _bin("*", _num(2.0), ("call", "sqrt", [u])))
        if name == "pow":
            return diff(_bin("^", args[0], args[1]), var)
    raise ValueError(f"cannot differentiate {ast!r}")


def simplify(ast):
    """Constant folding plus a few algebraic identities."""
    kind = ast[0]
    if kind in ("num", "var"):
        return ast
    if kind == "neg":
        e = simplify(ast[1])
        if e[0] == "num":
            return _num(-e[1])
        if e[0] == "neg":
            return e[1]
        return ("neg", e)
    if kind == "bin":
        op = ast[1]
        l = simplify(ast[2])
        r = simplify(ast[3])
        if l[0] == "num" and r[0] == "num":
            try:
                return _num({"+": lambda: l[1] + r[1],
                             "-": lambda: l[1] - r[1],
                             "*": lambda: l[1] * r[1],
                             "/": lambda: l[1] / r[1],
                             "^": lambda: l[1] ** r[1]}[op]())
            except (ArithmeticError, OverflowError):
                pass
        z, one = ("num", 0.0), ("num", 1.0)
        if op == "+":
            if l == z:
                return r
            if r == z:
                return l
        elif op == "-":
            if r == z:
                return l
        elif op == "*":
            if l == z or r == z:
                return z
            if l == one:
                return r
            if r == one:
                return l
            # fold numeric constants across nested products: a*(b*x) -> (ab)*x
            if l[0] == "num" and r[0] == "bin" and r[1] == "*" and r[2][0] == "num":
                return simplify(("bin", "*", _num(l[1] * r[2][1]), r[3]))
            if r[0] == "num" and l[0] == "bin" and l[1] == "*" and l[3][0] == "num":
                return simplify(("bin", "*", _num(r[1] * l[3][1]), l[2]))
        elif op == "/":
            if l == z:
                return z
            if r == one:
                return l
        elif op == "^":
            if r == z:
                return one
            if r == one:
                return l
            if l in (z, one):
                return l
        return ("bin", op, l, r)
    if kind == "call":
        return ("call", ast[1], [simplify(a) for a in ast[2]])
    raise ValueError(f"bad AST node {ast!r}")


def to_source(ast):
    """Render an AST back to parseable source (fully parenthesized)."""
    kind = ast[0]
    if kind == "num":
        return repr(ast[1])
    if kind == "var":
        return ast[1]
    if kind == "neg":
        return f"(-{to_source(ast[1])})"
    if kind == "bin":
        return f"({to_source(ast[2])} {ast[1]} {to_source(ast[3])})"
    if kind == "call":
        return f"{ast[1]}({', '.join(to_source(a) for a in ast[2])})"
    raise ValueError(f"bad AST node {ast!r}")


def diff_source(ast, var):
    """Convenience: symbolic derivative, simplified, as source text."""
    return to_source(simplify(diff(ast, var)))
