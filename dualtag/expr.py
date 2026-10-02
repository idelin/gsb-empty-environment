"""A tiny pure-expression interpreter.

Grammar (``^`` is right-associative and binds tighter than unary minus on
its left, so ``-x^2 == -(x^2)`` and ``2^-3`` works)::

    expr    := term (('+' | '-') term)*
    term    := factor (('*' | '/') factor)*
    factor  := unary
    unary   := ('-' | '+') unary | power
    power   := primary ('^' unary)?
    primary := number | name | name '(' args ')' | '(' expr ')'
    args    := expr (',' expr)*

Evaluation goes through :mod:`dualtag.dual`, so any environment value may
be a float or a (possibly nested) tagged Dual -- which is what lets inner
differentiations reference outer perturbed variables safely.
"""
from __future__ import annotations

import re

from . import dual as _D

_FUNCTIONS = {
    "sin": _D.sin, "cos": _D.cos, "tan": _D.tan, "exp": _D.exp,
    "log": _D.log, "sqrt": _D.sqrt, "abs": _D.abs_, "sign": _D.sign,
    "pow": _D.power, "min": _D.min_, "max": _D.max_,
}

_BINOPS = {"+": _D.add, "-": _D.sub, "*": _D.mul, "/": _D.div, "^": _D.power}

_TOKEN_RE = re.compile(r"""(?:
    (?P<num>(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)
  | (?P<name>[A-Za-z_]\w*)
  | (?P<op>[-+*/^(),])
)""", re.VERBOSE)


def tokenize(text):
    tokens = []
    pos = 0
    while pos < len(text):
        if text[pos].isspace():
            pos += 1
            continue
        m = _TOKEN_RE.match(text, pos)
        if not m:
            raise SyntaxError(f"unexpected character at position {pos}: {text[pos]!r}")
        pos = m.end()
        if m.group("num") is not None:
            tokens.append(("num", float(m.group("num"))))
        elif m.group("name") is not None:
            tokens.append(("name", m.group("name")))
        else:
            tokens.append(("op", m.group("op")))
    tokens.append(("end", None))
    return tokens


class _Parser:
    def __init__(self, tokens):
        self.toks = tokens
        self.i = 0

    def peek(self):
        return self.toks[self.i]

    def advance(self):
        tok = self.toks[self.i]
        self.i += 1
        return tok

    def expect_op(self, op):
        tok = self.advance()
        if tok != ("op", op):
            raise SyntaxError(f"expected {op!r}, got {tok!r}")

    def parse_expr(self):
        node = self.parse_term()
        while self.peek() in (("op", "+"), ("op", "-")):
            op = self.advance()[1]
            node = ("bin", op, node, self.parse_term())
        return node

    def parse_term(self):
        node = self.parse_unary()
        while self.peek() in (("op", "*"), ("op", "/")):
            op = self.advance()[1]
            node = ("bin", op, node, self.parse_unary())
        return node

    def parse_unary(self):
        if self.peek() == ("op", "-"):
            self.advance()
            return ("neg", self.parse_unary())
        if self.peek() == ("op", "+"):
            self.advance()
            return self.parse_unary()
        return self.parse_power()

    def parse_power(self):
        base = self.parse_primary()
        if self.peek() == ("op", "^"):
            self.advance()
            return ("bin", "^", base, self.parse_unary())
        return base

    def parse_primary(self):
        kind, value = self.advance()
        if kind == "num":
            return ("num", value)
        if kind == "name":
            if self.peek() == ("op", "("):
                self.advance()
                args = []
                if self.peek() != ("op", ")"):
                    args.append(self.parse_expr())
                    while self.peek() == ("op", ","):
                        self.advance()
                        args.append(self.parse_expr())
                self.expect_op(")")
                if value not in _FUNCTIONS:
                    raise NameError(f"unknown function {value!r}")
                return ("call", value, args)
            return ("var", value)
        if kind == "op" and value == "(":
            node = self.parse_expr()
            self.expect_op(")")
            return node
        raise SyntaxError(f"unexpected token {(kind, value)!r}")


def parse(text):
    """Parse source text into an AST of nested tuples."""
    parser = _Parser(tokenize(text))
    node = parser.parse_expr()
    if parser.peek()[0] != "end":
        raise SyntaxError(f"trailing input at token {parser.peek()!r}")
    return node


def evaluate(ast, env):
    """Evaluate an AST.  ``env`` values may be floats or tagged Duals."""
    kind = ast[0]
    if kind == "num":
        return ast[1]
    if kind == "var":
        name = ast[1]
        if name not in env:
            raise NameError(f"unknown variable {name!r}")
        return env[name]
    if kind == "neg":
        return _D.neg(evaluate(ast[1], env))
    if kind == "bin":
        left = evaluate(ast[2], env)
        right = evaluate(ast[3], env)
        return _BINOPS[ast[1]](left, right)
    if kind == "call":
        fn = _FUNCTIONS[ast[1]]
        return fn(*[evaluate(a, env) for a in ast[2]])
    raise ValueError(f"bad AST node {ast!r}")


def variables(ast):
    """Set of variable names occurring in the expression."""
    kind = ast[0]
    if kind == "var":
        return {ast[1]}
    if kind == "num":
        return set()
    if kind == "neg":
        return variables(ast[1])
    if kind == "bin":
        return variables(ast[2]) | variables(ast[3])
    if kind == "call":
        out = set()
        for a in ast[2]:
            out |= variables(a)
        return out
    return set()
