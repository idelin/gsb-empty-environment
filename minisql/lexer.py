"""Tokenizer for the mini-SQL subset."""
from __future__ import annotations

from dataclasses import dataclass

from .errors import SQLSyntaxError


@dataclass(frozen=True)
class Token:
    kind: str          # KEYWORD | IDENT | NUMBER | STRING | PUNCT
    value: str
    pos: int


_KEYWORDS = frozenset({
    "select", "from", "where", "group", "by", "as", "and", "or", "not",
    "is", "null", "exists", "count", "sum", "avg", "min", "max",
    "distinct", "all", "true", "false",
    "order", "by", "limit", "offset", "having", "union", "intersect",
    "except", "join", "inner", "left", "right", "full", "outer", "cross",
    "on", "using",
})

# Operators/punctuation, longest first so '>=' wins over '>'.
_PUNCT = ("<=", ">=", "<>", "!=", ",", ".", "(", ")", "*", "/", "+", "-",
          "<", ">", "=")


def tokenize(sql: str) -> list[Token]:
    tokens: list[Token] = []
    i, n = 0, len(sql)
    while i < n:
        ch = sql[i]
        if ch in " \t\r\n":
            i += 1
            continue
        if ch == "'":
            start = i
            i += 1
            buf: list[str] = []
            while i < n:
                c = sql[i]
                if c == "'":
                    # doubled quote '' is an escaped quote
                    if i + 1 < n and sql[i + 1] == "'":
                        buf.append("'")
                        i += 2
                        continue
                    i += 1
                    break
                buf.append(c)
                i += 1
            else:
                raise SQLSyntaxError("unterminated string literal at %d" % start)
            tokens.append(Token("STRING", "".join(buf), start))
            continue
        if ch.isdigit():
            start = i
            while i < n and sql[i].isdigit():
                i += 1
            if i < n and sql[i] == ".":
                i += 1
                if i >= n or not sql[i].isdigit():
                    raise SQLSyntaxError("malformed numeric literal at %d" % start)
                while i < n and sql[i].isdigit():
                    i += 1
            tokens.append(Token("NUMBER", sql[start:i], start))
            continue
        if ch.isalpha() or ch == "_":
            start = i
            while i < n and (sql[i].isalnum() or sql[i] == "_"):
                i += 1
            word = sql[start:i].lower()
            kind = "KEYWORD" if word in _KEYWORDS else "IDENT"
            tokens.append(Token(kind, word, start))
            continue
        for op in _PUNCT:
            if sql.startswith(op, i):
                tokens.append(Token("PUNCT", op, i))
                i += len(op)
                break
        else:
            raise SQLSyntaxError("unexpected character %r at %d" % (ch, i))
    tokens.append(Token("EOF", "", n))
    return tokens
