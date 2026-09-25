"""Hand-rolled recursive-descent parser (standard library only).

Grammar (loosely SQL-flavoured)::

    expr        := or_expr
    or_expr     := and_expr (OR and_expr)*
    and_expr    := not_expr (AND not_expr)*
    not_expr    := NOT not_expr | comparison
    comparison  := additive (('=' | '!=' | '<' | '<=' | '>' | '>=') additive)?
    additive    := multiplicative (('+' | '-') multiplicative)*
    multiplicative := unary (('*' | '/' | '%') unary)*
    unary       := '-' unary | primary
    primary     := NUMBER | TRUE | FALSE | NULL | IDENT
                 | IF '(' expr ',' expr ',' expr ')'
                 | CAST '(' expr AS TYPE ')'
                 | '(' expr ')'

Keywords are case-insensitive; column names are not.
"""

from __future__ import annotations

import re

from . import ast_nodes as ast
from .errors import CompileError

_TOKEN_RE = re.compile(
    r"""
      (?P<ws>\s+)
    | (?P<float>(?:\d+\.\d*|\.\d+)(?:[eE][+-]?\d+)?|\d+[eE][+-]?\d+)
    | (?P<int>\d+)
    | (?P<ident>[A-Za-z_][A-Za-z0-9_]*)
    | (?P<op><=|>=|!=|[-+*/%=<>(),])
    | (?P<bad>.)
    """,
    re.VERBOSE,
)

_KEYWORDS = {"AND", "OR", "NOT", "IF", "CAST", "AS", "TRUE", "FALSE", "NULL"}


class _Token:
    __slots__ = ("kind", "text", "pos")

    def __init__(self, kind: str, text: str, pos: int):
        self.kind = kind
        self.text = text
        self.pos = pos

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return f"Token({self.kind}, {self.text!r}, @{self.pos})"


def _tokenize(source: str) -> list[_Token]:
    tokens: list[_Token] = []
    for m in _TOKEN_RE.finditer(source):
        kind = m.lastgroup
        text = m.group()
        if kind == "ws":
            continue
        if kind == "bad":
            raise CompileError(
                f"unexpected character {text!r} at offset {m.start()} "
                f"in {source!r}"
            )
        if kind == "ident" and text.upper() in _KEYWORDS:
            kind = text.upper()
        tokens.append(_Token(kind, text, m.start()))
    tokens.append(_Token("eof", "", len(source)))
    return tokens


class _Parser:
    def __init__(self, source: str):
        self.source = source
        self.tokens = _tokenize(source)
        self.i = 0

    @property
    def cur(self) -> _Token:
        return self.tokens[self.i]

    def _advance(self) -> _Token:
        tok = self.tokens[self.i]
        self.i += 1
        return tok

    def _accept(self, *kinds: str) -> _Token | None:
        if self.cur.kind in kinds or self.cur.text in kinds:
            return self._advance()
        return None

    def _expect(self, kind: str) -> _Token:
        tok = self._accept(kind)
        if tok is None:
            raise CompileError(
                f"expected {kind!r} but found {self.cur.text!r} "
                f"at offset {self.cur.pos} in {self.source!r}"
            )
        return tok

    def parse(self) -> ast.Expr:
        expr = self._or()
        if self.cur.kind != "eof":
            raise CompileError(
                f"unexpected trailing input {self.cur.text!r} at offset "
                f"{self.cur.pos} in {self.source!r}"
            )
        return expr

    def _or(self) -> ast.Expr:
        left = self._and()
        while (tok := self._accept("OR")) is not None:
            left = ast.BinaryOp(tok.pos, "OR", left, self._and())
        return left

    def _and(self) -> ast.Expr:
        left = self._not()
        while (tok := self._accept("AND")) is not None:
            left = ast.BinaryOp(tok.pos, "AND", left, self._not())
        return left

    def _not(self) -> ast.Expr:
        if (tok := self._accept("NOT")) is not None:
            return ast.UnaryOp(tok.pos, "NOT", self._not())
        return self._comparison()

    def _comparison(self) -> ast.Expr:
        left = self._additive()
        tok = self._accept("=", "!=", "<", "<=", ">", ">=")
        if tok is not None:
            return ast.BinaryOp(tok.pos, tok.text, left, self._additive())
        return left

    def _additive(self) -> ast.Expr:
        left = self._multiplicative()
        while (tok := self._accept("+", "-")) is not None:
            left = ast.BinaryOp(tok.pos, tok.text, left, self._multiplicative())
        return left

    def _multiplicative(self) -> ast.Expr:
        left = self._unary()
        while (tok := self._accept("*", "/", "%")) is not None:
            left = ast.BinaryOp(tok.pos, tok.text, left, self._unary())
        return left

    def _unary(self) -> ast.Expr:
        if (tok := self._accept("-")) is not None:
            return ast.UnaryOp(tok.pos, "-", self._unary())
        return self._primary()

    def _primary(self) -> ast.Expr:
        tok = self.cur
        if self._accept("int"):
            return ast.Literal(tok.pos, int(tok.text))
        if self._accept("float"):
            return ast.Literal(tok.pos, float(tok.text))
        if self._accept("TRUE"):
            return ast.Literal(tok.pos, True)
        if self._accept("FALSE"):
            return ast.Literal(tok.pos, False)
        if self._accept("NULL"):
            return ast.Literal(tok.pos, None)
        if self._accept("ident"):
            return ast.ColumnRef(tok.pos, tok.text)
        if self._accept("IF"):
            self._expect("(")
            cond = self._or()
            self._expect(",")
            then = self._or()
            self._expect(",")
            otherwise = self._or()
            self._expect(")")
            return ast.If(tok.pos, cond, then, otherwise)
        if self._accept("CAST"):
            self._expect("(")
            operand = self._or()
            self._expect("AS")
            type_tok = self._expect("ident")
            self._expect(")")
            return ast.Cast(tok.pos, operand, type_tok.text.upper())
        if self._accept("("):
            expr = self._or()
            self._expect(")")
            return expr
        raise CompileError(
            f"unexpected {tok.text!r} at offset {tok.pos} in {self.source!r}"
        )


def parse(source: str) -> ast.Expr:
    """Parse ``source`` into an untyped AST (raises CompileError)."""
    return _Parser(source).parse()
