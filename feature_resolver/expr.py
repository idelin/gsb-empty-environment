"""Boolean expressions over feature names: &&, ||, !, parentheses."""

from __future__ import annotations

import re

_TOKEN_RE = re.compile(
    r"\s*(?:(&&)|(\|\|)|(!)|(\()|(\))|([A-Za-z_][A-Za-z0-9_-]*))"
)


class ExprError(ValueError):
    pass


def _tokenize(text):
    pos = 0
    tokens = []
    while pos < len(text):
        m = _TOKEN_RE.match(text, pos)
        if not m:
            raise ExprError(f"unexpected character at {pos} in expression {text!r}")
        pos = m.end()
        if m.group(1):
            tokens.append(("and", "&&"))
        elif m.group(2):
            tokens.append(("or", "||"))
        elif m.group(3):
            tokens.append(("not", "!"))
        elif m.group(4):
            tokens.append(("lparen", "("))
        elif m.group(5):
            tokens.append(("rparen", ")"))
        else:
            tokens.append(("ident", m.group(6)))
    tokens.append(("end", ""))
    return tokens


class _Parser:
    def __init__(self, text):
        self.tokens = _tokenize(text)
        self.pos = 0

    def peek(self):
        return self.tokens[self.pos]

    def advance(self):
        tok = self.tokens[self.pos]
        self.pos += 1
        return tok

    def expect(self, kind):
        tok = self.advance()
        if tok[0] != kind:
            raise ExprError(f"expected {kind}, got {tok[1]!r}")
        return tok

    def parse(self):
        node = self.parse_or()
        if self.peek()[0] != "end":
            raise ExprError(f"trailing token {self.peek()[1]!r} in expression")
        return node

    def parse_or(self):
        node = self.parse_and()
        while self.peek()[0] == "or":
            self.advance()
            node = ("or", node, self.parse_and())
        return node

    def parse_and(self):
        node = self.parse_not()
        while self.peek()[0] == "and":
            self.advance()
            node = ("and", node, self.parse_not())
        return node

    def parse_not(self):
        if self.peek()[0] == "not":
            self.advance()
            return ("not", self.parse_not())
        return self.parse_atom()

    def parse_atom(self):
        kind, value = self.peek()
        if kind == "ident":
            self.advance()
            return ("ident", value)
        if kind == "lparen":
            self.advance()
            node = self.parse_or()
            self.expect("rparen")
            return node
        raise ExprError(f"unexpected token {value!r} in expression")


def parse(text):
    """Parse an expression string into an AST tuple."""
    return _Parser(text).parse()


def evaluate(node, active):
    """Evaluate an AST against a set of active feature names."""
    kind = node[0]
    if kind == "ident":
        return node[1] in active
    if kind == "not":
        return not evaluate(node[1], active)
    if kind == "and":
        return evaluate(node[1], active) and evaluate(node[2], active)
    if kind == "or":
        return evaluate(node[1], active) or evaluate(node[2], active)
    raise ExprError(f"unknown node kind {kind!r}")


def names(node):
    """Collect all feature identifiers referenced by an AST."""
    kind = node[0]
    if kind == "ident":
        return {node[1]}
    if kind == "not":
        return names(node[1])
    return names(node[1]) | names(node[2])
