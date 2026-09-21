"""Lexer: source text -> tokens with UTF-8 byte spans.

Spans are half-open byte intervals ``(start, end)`` into the UTF-8
encoding of the source, not character indices.
"""

from __future__ import annotations

from dataclasses import dataclass

from .errors import LexError

KEYWORDS = {"let": "LET", "in": "IN"}
PUNCT = {
    "+": "PLUS",
    "-": "MINUS",
    "*": "STAR",
    "/": "SLASH",
    "(": "LPAREN",
    ")": "RPAREN",
    "=": "EQUALS",
    ",": "COMMA",
}


@dataclass(frozen=True)
class Token:
    kind: str
    text: str
    span: tuple[int, int]


def byte_offsets(src: str) -> list[int]:
    """Map each character index to its UTF-8 byte offset."""
    offsets = [0]
    for ch in src:
        offsets.append(offsets[-1] + len(ch.encode("utf-8")))
    return offsets


def tokenize(src: str) -> list[Token]:
    offsets = byte_offsets(src)
    tokens: list[Token] = []
    i = 0
    n = len(src)
    while i < n:
        ch = src[i]
        if ch in " \t\r\n":
            i += 1
            continue
        if ch == "#":  # comment to end of line
            while i < n and src[i] != "\n":
                i += 1
            continue
        start = i
        if ch.isdigit():
            while i < n and src[i].isdigit():
                i += 1
            tokens.append(Token("INT", src[start:i], (offsets[start], offsets[i])))
        elif ch.isalpha() or ch == "_":
            while i < n and (src[i].isalnum() or src[i] == "_"):
                i += 1
            text = src[start:i]
            tokens.append(Token(KEYWORDS.get(text, "IDENT"), text, (offsets[start], offsets[i])))
        elif ch in PUNCT:
            i += 1
            tokens.append(Token(PUNCT[ch], ch, (offsets[start], offsets[i])))
        else:
            raise LexError(f"unexpected character {ch!r}", (offsets[i], offsets[i + 1]))
    tokens.append(Token("EOF", "", (offsets[n], offsets[n])))
    return tokens
