"""Lexer for the mini language.

All source positions are UTF-8 *byte* offsets (half-open [start, end) ranges),
not character indices. The lexer walks the decoded ``str`` but accumulates the
byte offset of every character so spans stay correct even when the source
contains multi-byte characters (e.g. comments in CJK).
"""

from __future__ import annotations

from dataclasses import dataclass

KEYWORDS = {"let", "if", "else", "while", "output", "input"}

MULTI_OPS = ("==", "!=", "<=", ">=")
SINGLE_OPS = set("+-*/%<>")
PUNCT = set("(){};=")


@dataclass(frozen=True)
class Token:
    kind: str  # 'INT' | 'IDENT' | 'KEYWORD' | 'OP' | 'PUNCT' | 'EOF'
    text: str
    start: int  # UTF-8 byte offset, inclusive
    end: int  # UTF-8 byte offset, exclusive

    def span(self) -> tuple[int, int]:
        return (self.start, self.end)


class LexError(Exception):
    def __init__(self, message: str, span: tuple[int, int]):
        super().__init__(f"lex error: {message} at byte range [{span[0]}, {span[1]})")
        self.span = span


def tokenize(source: str) -> list[Token]:
    tokens: list[Token] = []
    i = 0
    byte = 0
    n = len(source)

    def advance() -> str:
        nonlocal i, byte
        ch = source[i]
        i += 1
        byte += len(ch.encode("utf-8"))
        return ch

    while i < n:
        ch = source[i]
        if ch.isspace():
            advance()
            continue
        if ch == "#":  # comment to end of line
            while i < n and source[i] != "\n":
                advance()
            continue
        start = byte
        if ch.isdigit():
            j = i
            while i < n and source[i].isdigit():
                advance()
            tokens.append(Token("INT", source[j:i], start, byte))
            continue
        if ch.isalpha() or ch == "_":
            j = i
            while i < n and (source[i].isalnum() or source[i] == "_"):
                advance()
            text = source[j:i]
            kind = "KEYWORD" if text in KEYWORDS else "IDENT"
            tokens.append(Token(kind, text, start, byte))
            continue
        if source[i : i + 2] in MULTI_OPS:
            text = source[i : i + 2]
            advance()
            advance()
            tokens.append(Token("OP", text, start, byte))
            continue
        if ch in SINGLE_OPS:
            advance()
            tokens.append(Token("OP", ch, start, byte))
            continue
        if ch in PUNCT:
            advance()
            tokens.append(Token("PUNCT", ch, start, byte))
            continue
        raise LexError(
            f"unexpected character {ch!r}", (start, start + len(ch.encode("utf-8")))
        )
    tokens.append(Token("EOF", "", byte, byte))
    return tokens
