"""Hand-written lexer. All tokens carry UTF-8 byte spans (no eval anywhere)."""
from __future__ import annotations

from dataclasses import dataclass

from .diag import Diagnostic, Span


@dataclass(frozen=True)
class Token:
    kind: str  # 'ident' | 'int' | 'punct' | 'eof'
    text: str
    span: Span


_SINGLE_PUNCT = "+-*/%()[]{}=;,.!"
_DOUBLE_PUNCT = ("&&", "||")


def _byte_len(ch: str) -> int:
    return len(ch.encode("utf-8"))


def lex(source: str, module: str, diags: list[Diagnostic]) -> list[Token]:
    tokens: list[Token] = []
    i = 0
    b = 0  # current UTF-8 byte offset
    n = len(source)
    while i < n:
        ch = source[i]
        w = _byte_len(ch)
        if ch.isspace():
            i += 1
            b += w
            continue
        if ch == "#" or (ch == "/" and i + 1 < n and source[i + 1] == "/"):
            while i < n and source[i] != "\n":
                b += _byte_len(source[i])
                i += 1
            continue
        start = b
        if ch.isalpha() or ch == "_":
            j = i
            while j < n and (source[j].isalnum() or source[j] == "_"):
                j += 1
            text = source[i:j]
            b += len(text.encode("utf-8"))
            i = j
            tokens.append(Token("ident", text, Span(start, b)))
            continue
        if ch.isdigit():
            j = i
            while j < n and source[j].isdigit():
                j += 1
            text = source[i:j]
            b += len(text.encode("utf-8"))
            i = j
            tokens.append(Token("int", text, Span(start, b)))
            continue
        two = source[i : i + 2]
        if two in _DOUBLE_PUNCT:
            tokens.append(Token("punct", two, Span(start, start + 2)))
            i += 2
            b += 2
            continue
        if ch in _SINGLE_PUNCT:
            tokens.append(Token("punct", ch, Span(start, start + 1)))
            i += 1
            b += 1
            continue
        diags.append(
            Diagnostic(module, Span(start, start + w), "lex-error", f"unexpected character {ch!r}")
        )
        i += 1
        b += w
    tokens.append(Token("eof", "", Span(b, b)))
    return tokens
