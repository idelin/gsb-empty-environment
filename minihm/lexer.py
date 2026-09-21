"""Hand-written lexer producing tokens with UTF-8 byte spans."""
from __future__ import annotations

from dataclasses import dataclass

from .errors import LexError
from .source import Source, Span

KEYWORDS = {"fun", "let", "in", "if", "then", "else", "true", "false"}
_SINGLE_OPS = "+-*<=()"


@dataclass
class Token:
    kind: str  # INT IDENT KW OP LP RP EOF
    text: str
    span: Span


def lex(src: Source) -> list[Token]:
    text = src.text
    i, n = 0, len(text)
    tokens: list[Token] = []
    while i < n:
        ch = text[i]
        if ch.isspace():
            i += 1
            continue
        if ch == "#":  # comment to end of line
            while i < n and text[i] != "\n":
                i += 1
            continue
        start = i
        if ch.isdigit():
            while i < n and text[i].isdigit():
                i += 1
            tokens.append(Token("INT", text[start:i], src.byte_span(start, i)))
        elif ch.isalpha() or ch == "_":
            while i < n and (text[i].isalnum() or text[i] in "_'"):
                i += 1
            word = text[start:i]
            kind = "KW" if word in KEYWORDS else "IDENT"
            tokens.append(Token(kind, word, src.byte_span(start, i)))
        elif text.startswith("->", i) or text.startswith("==", i):
            tokens.append(Token("OP", text[i:i + 2], src.byte_span(i, i + 2)))
            i += 2
        elif ch in _SINGLE_OPS:
            kind = {"(": "LP", ")": "RP"}.get(ch, "OP")
            tokens.append(Token(kind, ch, src.byte_span(i, i + 1)))
            i += 1
        else:
            raise LexError(f"unexpected character {ch!r}", src.byte_span(i, i + 1))
    tokens.append(Token("EOF", "", src.byte_span(n, n)))
    return tokens
