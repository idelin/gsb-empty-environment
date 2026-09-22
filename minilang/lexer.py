"""Lexer for the mini language.

All positions are UTF-8 *byte* offsets into the source. The lexer works
directly on ``bytes`` so spans never need re-encoding.
"""
from __future__ import annotations

from dataclasses import dataclass

FN = "FN"
LET = "LET"
IF = "IF"
THEN = "THEN"
ELSE = "ELSE"
IDENT = "IDENT"
NUMBER = "NUMBER"
LPAREN = "LPAREN"
RPAREN = "RPAREN"
COMMA = "COMMA"
EQUALS = "EQUALS"
PLUS = "PLUS"
MINUS = "MINUS"
STAR = "STAR"
SLASH = "SLASH"
EQEQ = "EQEQ"
NEQ = "NEQ"
LT = "LT"
LE = "LE"
GT = "GT"
GE = "GE"
EOF = "EOF"
ERROR = "ERROR"

KEYWORDS = {"fn": FN, "let": LET, "if": IF, "then": THEN, "else": ELSE}

__all__ = [
    "Token", "lex", "KEYWORDS",
    "FN", "LET", "IF", "THEN", "ELSE", "IDENT", "NUMBER",
    "LPAREN", "RPAREN", "COMMA", "EQUALS", "PLUS", "MINUS", "STAR", "SLASH",
    "EQEQ", "NEQ", "LT", "LE", "GT", "GE", "EOF", "ERROR",
]

_WHITESPACE = b" \t\r\n"
_HASH = 0x23
_NEWLINE = 0x0A
_UNDERSCORE = 0x5F
_DOT = 0x2E


def _is_digit(b: int) -> bool:
    return 0x30 <= b <= 0x39


def _is_alpha(b: int) -> bool:
    return b == _UNDERSCORE or 0x41 <= b <= 0x5A or 0x61 <= b <= 0x7A


@dataclass(frozen=True)
class Token:
    kind: str
    text: str
    start: int  # UTF-8 byte offset, inclusive
    end: int    # UTF-8 byte offset, exclusive

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return f"Token({self.kind}, {self.text!r}, [{self.start}, {self.end}))"


def lex(src: bytes) -> list[Token]:
    """Tokenize ``src`` (UTF-8 bytes) into tokens with byte spans.

    Whitespace and ``#`` line comments are skipped. Bytes that do not form
    any token become single-byte ``ERROR`` tokens so the parser can report
    and recover from them.
    """
    tokens: list[Token] = []
    i = 0
    n = len(src)
    while i < n:
        b = src[i]
        if b in _WHITESPACE:
            i += 1
            continue
        if b == _HASH:
            while i < n and src[i] != _NEWLINE:
                i += 1
            continue
        start = i
        if _is_digit(b):
            i += 1
            while i < n and _is_digit(src[i]):
                i += 1
            if (i < n and src[i] == _DOT and i + 1 < n
                    and _is_digit(src[i + 1])):
                i += 2
                while i < n and _is_digit(src[i]):
                    i += 1
            tokens.append(Token(NUMBER, src[start:i].decode("utf-8"), start, i))
            continue
        if _is_alpha(b):
            i += 1
            while i < n and (_is_alpha(src[i]) or _is_digit(src[i])):
                i += 1
            text = src[start:i].decode("utf-8")
            tokens.append(Token(KEYWORDS.get(text, IDENT), text, start, i))
            continue
        two = src[i:i + 2]
        if two == b"==":
            tokens.append(Token(EQEQ, "==", start, start + 2))
            i += 2
            continue
        if two == b"!=":
            tokens.append(Token(NEQ, "!=", start, start + 2))
            i += 2
            continue
        if two == b"<=":
            tokens.append(Token(LE, "<=", start, start + 2))
            i += 2
            continue
        if two == b">=":
            tokens.append(Token(GE, ">=", start, start + 2))
            i += 2
            continue
        single = {
            ord("("): LPAREN, ord(")"): RPAREN, ord(","): COMMA,
            ord("="): EQUALS, ord("+"): PLUS, ord("-"): MINUS,
            ord("*"): STAR, ord("/"): SLASH,
            ord("<"): LT, ord(">"): GT,
        }.get(b)
        if single is not None:
            tokens.append(Token(single, chr(b), start, start + 1))
            i += 1
            continue
        # Anything else (including stray UTF-8 continuation bytes) is an error.
        tokens.append(Token(ERROR, src[start:start + 1].decode("utf-8", "replace"),
                            start, start + 1))
        i += 1
    tokens.append(Token(EOF, "", n, n))
    return tokens
