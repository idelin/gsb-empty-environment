"""Lexer over the UTF-8 encoding of the source.

Tokens carry byte offsets directly, so spans are valid UTF-8 byte
ranges even when comments contain multi-byte characters.
"""
from __future__ import annotations

from dataclasses import dataclass

KEYWORDS = frozenset({"let", "if", "else", "while", "print", "halt"})

_TWO_CHAR = ("==", "!=", "<=", ">=")
_ONE_CHAR = "+-*/%(){};=<>!"


@dataclass(frozen=True)
class Token:
    kind: str  # "ident" | "int" | "keyword" | "punct" | "eof"
    text: str
    start: int  # UTF-8 byte offset, inclusive
    end: int  # UTF-8 byte offset, exclusive


class LexError(Exception):
    def __init__(self, message: str, start: int, end: int):
        super().__init__(message)
        self.start = start
        self.end = end


def _is_alpha(b: int) -> bool:
    return 0x41 <= b <= 0x5A or 0x61 <= b <= 0x7A or b == 0x5F


def _is_digit(b: int) -> bool:
    return 0x30 <= b <= 0x39


def lex(source: str) -> list[Token]:
    data = source.encode("utf-8")
    tokens: list[Token] = []
    i, n = 0, len(data)
    while i < n:
        b = data[i]
        if b in (0x20, 0x09, 0x0D, 0x0A):
            i += 1
            continue
        if data[i : i + 2] == b"//":
            while i < n and data[i] != 0x0A:
                i += 1
            continue
        if b >= 0x80:
            raise LexError(f"non-ASCII byte 0x{b:02x} outside comment", i, i + 1)
        if _is_alpha(b):
            j = i + 1
            while j < n and (_is_alpha(data[j]) or _is_digit(data[j])):
                j += 1
            text = data[i:j].decode("ascii")
            kind = "keyword" if text in KEYWORDS else "ident"
            tokens.append(Token(kind, text, i, j))
            i = j
            continue
        if _is_digit(b):
            j = i + 1
            while j < n and _is_digit(data[j]):
                j += 1
            tokens.append(Token("int", data[i:j].decode("ascii"), i, j))
            i = j
            continue
        two = data[i : i + 2].decode("ascii")
        if two in _TWO_CHAR:
            tokens.append(Token("punct", two, i, i + 2))
            i += 2
            continue
        ch = chr(b)
        if ch in _ONE_CHAR:
            tokens.append(Token("punct", ch, i, i + 1))
            i += 1
            continue
        raise LexError(f"unexpected character {ch!r}", i, i + 1)
    tokens.append(Token("eof", "", n, n))
    return tokens
