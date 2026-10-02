"""Boolean expression parsing, evaluation and BDD compilation.

AST nodes are tuples:
    ('const', bool) | ('var', name) | ('not', a) | (op, a, b)
with op in {'and', 'or', 'xor', 'implies', 'iff'}.

Concrete evaluation through :func:`eval_ast` is deliberately independent
of the BDD engine so certificates can be replayed without trusting it.
"""

from __future__ import annotations

import re

_TOKEN = re.compile(
    r"<->|<=>|->|=>|&&|\|\||[()!~&^|]|[A-Za-z_][A-Za-z0-9_]*|\d+"
)
_KEYWORDS = {"and", "or", "not", "xor", "true", "false"}


class ParseError(ValueError):
    pass


def tokenize(text):
    tokens = []
    pos = 0
    n = len(text)
    while True:
        while pos < n and text[pos].isspace():
            pos += 1
        if pos >= n:
            break
        m = _TOKEN.match(text, pos)
        if not m:
            raise ParseError(f"unexpected character at column {pos}: {text[pos]!r}")
        tokens.append(m.group(0))
        pos = m.end()
    return tokens


class _Parser:
    def __init__(self, tokens):
        self.toks = tokens
        self.i = 0

    def peek(self):
        return self.toks[self.i] if self.i < len(self.toks) else None

    def next(self):
        tok = self.peek()
        if tok is None:
            raise ParseError("unexpected end of expression")
        self.i += 1
        return tok

    def expect(self, tok):
        got = self.next()
        if got != tok:
            raise ParseError(f"expected {tok!r}, got {got!r}")


def parse(text):
    """Parse a boolean expression into an AST.

    Precedence (tightest first): ! ~ not, & and, ^ xor, | or, -> =>, <-> <=>.
    Implication is right-associative.
    """
    parser = _Parser(tokenize(text))
    ast = _iff(parser)
    if parser.peek() is not None:
        raise ParseError(f"unexpected token {parser.peek()!r}")
    return ast


def _iff(p):
    left = _implies(p)
    while p.peek() in ("<->", "<=>"):
        p.next()
        left = ("iff", left, _implies(p))
    return left


def _implies(p):
    left = _or(p)
    if p.peek() in ("->", "=>"):
        p.next()
        return ("implies", left, _implies(p))
    return left


def _or(p):
    left = _xor(p)
    while p.peek() in ("|", "||", "or"):
        p.next()
        left = ("or", left, _xor(p))
    return left


def _xor(p):
    left = _and(p)
    while p.peek() in ("^", "xor"):
        p.next()
        left = ("xor", left, _and(p))
    return left


def _and(p):
    left = _unary(p)
    while p.peek() in ("&", "&&", "and"):
        p.next()
        left = ("and", left, _unary(p))
    return left


def _unary(p):
    if p.peek() in ("!", "~", "not"):
        p.next()
        return ("not", _unary(p))
    return _atom(p)


def _atom(p):
    tok = p.next()
    if tok == "(":
        ast = _iff(p)
        p.expect(")")
        return ast
    if tok in ("true", "1"):
        return ("const", True)
    if tok in ("false", "0"):
        return ("const", False)
    if re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", tok) and tok not in _KEYWORDS:
        return ("var", tok)
    raise ParseError(f"unexpected token {tok!r}")


def eval_ast(ast, assignment):
    """Evaluate an AST on a concrete assignment (no BDD machinery)."""
    tag = ast[0]
    if tag == "const":
        return bool(ast[1])
    if tag == "var":
        if ast[1] not in assignment:
            raise KeyError(f"no value for variable {ast[1]!r}")
        return bool(assignment[ast[1]])
    if tag == "not":
        return not eval_ast(ast[1], assignment)
    a = eval_ast(ast[1], assignment)
    b = eval_ast(ast[2], assignment)
    if tag == "and":
        return a and b
    if tag == "or":
        return a or b
    if tag == "xor":
        return a != b
    if tag == "implies":
        return (not a) or b
    if tag == "iff":
        return a == b
    raise ValueError(f"unknown AST node {tag!r}")


def ast_vars(ast):
    tag = ast[0]
    if tag == "const":
        return set()
    if tag == "var":
        return {ast[1]}
    if tag == "not":
        return ast_vars(ast[1])
    return ast_vars(ast[1]) | ast_vars(ast[2])


def build(ast, mgr):
    """Compile an AST into the shared BDD manager."""
    tag = ast[0]
    if tag == "const":
        return mgr.constant(ast[1])
    if tag == "var":
        if ast[1] not in mgr.level:
            raise ValueError(f"unknown variable {ast[1]!r}")
        return mgr.var(ast[1])
    if tag == "not":
        return mgr.negate(build(ast[1], mgr))
    if tag in ("and", "or", "xor", "implies", "iff"):
        return mgr.apply(tag, build(ast[1], mgr), build(ast[2], mgr))
    raise ValueError(f"unknown AST node {tag!r}")
