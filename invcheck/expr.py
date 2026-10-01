"""Boolean expression parsing and evaluation.

Grammar (loosest to tightest binding)::

    iff  := imp (("<->" | "<=>") imp)*
    imp  := disj (("->" | "=>") imp)?          # right associative
    disj := xor (("|" | "||" | "or") xor)*
    xor  := conj (("^" | "xor") conj)*
    conj := unary (("&" | "&&" | "and") unary)*
    unary := ("~" | "!" | "not") unary | atom
    atom := IDENT[']* | "0" | "1" | "true" | "false" | "(" iff ")"

Next-state variables are written with a trailing prime, e.g. ``b0'``.

AST nodes are tuples: ("const", bool), ("var", name),
("not", a), ("and"|"or"|"xor"|"imp"|"iff", a, b).
"""

from __future__ import annotations

import re

_TOKEN_RE = re.compile(
    r"""
      (?P<ws>\s+)
    | (?P<iff><->|<=>)
    | (?P<imp>->|=>)
    | (?P<ident>[A-Za-z_][A-Za-z0-9_]*'*)
    | (?P<lpar>\()
    | (?P<rpar>\))
    | (?P<and>&{1,2})
    | (?P<or>\|{1,2})
    | (?P<xor>\^)
    | (?P<not>[~!])
    | (?P<const>[01])
    """,
    re.VERBOSE,
)

_KEYWORDS = {"and": "and", "or": "or", "xor": "xor", "not": "not"}


class ParseError(ValueError):
    pass


def tokenize(text):
    tokens = []
    pos = 0
    while pos < len(text):
        m = _TOKEN_RE.match(text, pos)
        if m is None:
            raise ParseError("unexpected character at %d: %r" % (pos, text[pos]))
        pos = m.end()
        kind = m.lastgroup
        value = m.group()
        if kind == "ws":
            continue
        if kind == "ident" and value in _KEYWORDS:
            kind = _KEYWORDS[value]
        tokens.append((kind, value))
    tokens.append(("eof", ""))
    return tokens


class _Parser:
    def __init__(self, tokens):
        self.tokens = tokens
        self.pos = 0

    def peek(self):
        return self.tokens[self.pos][0]

    def next(self):
        tok = self.tokens[self.pos]
        self.pos += 1
        return tok

    def expect(self, kind):
        tok = self.next()
        if tok[0] != kind:
            raise ParseError("expected %s, got %r" % (kind, tok[1]))
        return tok

    def parse_iff(self):
        left = self.parse_imp()
        while self.peek() == "iff":
            self.next()
            left = ("iff", left, self.parse_imp())
        return left

    def parse_imp(self):
        left = self.parse_disj()
        if self.peek() == "imp":
            self.next()
            return ("imp", left, self.parse_imp())
        return left

    def parse_disj(self):
        left = self.parse_xor()
        while self.peek() == "or":
            self.next()
            left = ("or", left, self.parse_xor())
        return left

    def parse_xor(self):
        left = self.parse_conj()
        while self.peek() == "xor":
            self.next()
            left = ("xor", left, self.parse_conj())
        return left

    def parse_conj(self):
        left = self.parse_unary()
        while self.peek() == "and":
            self.next()
            left = ("and", left, self.parse_unary())
        return left

    def parse_unary(self):
        if self.peek() == "not":
            self.next()
            return ("not", self.parse_unary())
        return self.parse_atom()

    def parse_atom(self):
        kind, value = self.next()
        if kind == "const":
            return ("const", value == "1")
        if kind == "ident":
            if value == "true":
                return ("const", True)
            if value == "false":
                return ("const", False)
            return ("var", value)
        if kind == "lpar":
            node = self.parse_iff()
            self.expect("rpar")
            return node
        raise ParseError("unexpected token %r" % (value,))


def parse(text):
    """Parse a Boolean expression string into an AST."""
    parser = _Parser(tokenize(text))
    ast = parser.parse_iff()
    if parser.peek() != "eof":
        raise ParseError("trailing input: %r" % (parser.tokens[parser.pos][1],))
    return ast


def eval_ast(ast, assignment):
    """Evaluate an AST under a complete (or default-False) assignment."""
    op = ast[0]
    if op == "const":
        return ast[1]
    if op == "var":
        return bool(assignment.get(ast[1], False))
    if op == "not":
        return not eval_ast(ast[1], assignment)
    a, b = eval_ast(ast[1], assignment), None
    if op == "and":
        return a and eval_ast(ast[2], assignment)
    if op == "or":
        return a or eval_ast(ast[2], assignment)
    b = eval_ast(ast[2], assignment)
    if op == "xor":
        return a != b
    if op == "imp":
        return (not a) or b
    if op == "iff":
        return a == b
    raise ValueError("unknown AST op %r" % (op,))


def ast_variables(ast):
    if ast[0] == "const":
        return set()
    if ast[0] == "var":
        return {ast[1]}
    if ast[0] == "not":
        return ast_variables(ast[1])
    return ast_variables(ast[1]) | ast_variables(ast[2])


def to_bdd(mgr, ast):
    """Compile an AST into a BDD in ``mgr``."""
    op = ast[0]
    if op == "const":
        return mgr.TRUE if ast[1] else mgr.FALSE
    if op == "var":
        return mgr.var(ast[1])
    if op == "not":
        return mgr.negate(to_bdd(mgr, ast[1]))
    left = to_bdd(mgr, ast[1])
    right = to_bdd(mgr, ast[2])
    if op == "and":
        return mgr.and_(left, right)
    if op == "or":
        return mgr.or_(left, right)
    if op == "xor":
        return mgr.xor(left, right)
    if op == "imp":
        return mgr.implies(left, right)
    if op == "iff":
        return mgr.iff(left, right)
    raise ValueError("unknown AST op %r" % (op,))


def cube_to_expr(cube):
    """Render a partial assignment as a conjunction string."""
    if not cube:
        return "1"
    lits = []
    for name in sorted(cube):
        lits.append(name if cube[name] else "~" + name)
    return " & ".join(lits)
