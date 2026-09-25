"""Hand-written lexer producing tokens with UTF-8 byte spans."""
from __future__ import annotations

from .diagnostics import Diagnostic
from .tokens import (EOF, EQ, IDENT, LPAREN, MINUS, NUMBER, PLUS, RPAREN,
                     SEMI, SLASH, STAR, STRING, Token)

_KEYWORDS = {"let": "LET"}
_SINGLE = {"=": EQ, ";": SEMI, "+": PLUS, "-": MINUS, "*": STAR,
           "/": SLASH, "(": LPAREN, ")": RPAREN}
_ESCAPES = {"n": "\n", "t": "\t", '"': '"', "\\": "\\"}


def decode_string(text):
    inner = text[1:-1] if len(text) >= 2 and text.endswith('"') else text[1:]
    out = []
    i = 0
    while i < len(inner):
        ch = inner[i]
        if ch == "\\" and i + 1 < len(inner):
            out.append(_ESCAPES.get(inner[i + 1], inner[i + 1]))
            i += 2
        else:
            out.append(ch)
            i += 1
    return "".join(out)


def _byte_offsets(source):
    offsets = [0]
    total = 0
    for ch in source:
        total += len(ch.encode("utf-8"))
        offsets.append(total)
    return offsets


def lex(source):
    offsets = _byte_offsets(source)
    tokens = []
    diagnostics = []
    i = 0
    n = len(source)

    def emit(kind, first, last):
        index = len(tokens)
        tokens.append(Token(kind, source[first:last], offsets[first],
                            offsets[last], index, False, index))

    while i < n:
        ch = source[i]
        if ch.isspace():
            i += 1
            continue
        if ch == "#":
            while i < n and source[i] != "\n":
                i += 1
            continue
        if ch.isalpha() or ch == "_":
            j = i + 1
            while j < n and (source[j].isalnum() or source[j] == "_"):
                j += 1
            emit(_KEYWORDS.get(source[i:j], IDENT), i, j)
            i = j
            continue
        if ch.isdigit():
            j = i + 1
            while j < n and (source[j].isdigit() or source[j] == "."):
                j += 1
            emit(NUMBER, i, j)
            i = j
            continue
        if ch == '"':
            j = i + 1
            closed = False
            while j < n:
                cj = source[j]
                if cj == "\\" and j + 1 < n:
                    j += 2
                    continue
                if cj == '"':
                    closed = True
                    j += 1
                    break
                if cj == "\n":
                    break
                j += 1
            if not closed:
                # One diagnostic for the whole literal, never one per char.
                diagnostics.append(Diagnostic(
                    "unterminated string literal", offsets[i], offsets[j],
                    source="lexer"))
            emit(STRING, i, j)
            i = j
            continue
        kind = _SINGLE.get(ch)
        if kind is not None:
            emit(kind, i, i + 1)
            i += 1
            continue
        # Group a run of unexpected characters into a single diagnostic.
        j = i + 1
        while j < n and not (source[j].isspace() or source[j].isalnum()
                             or source[j] in _SINGLE or source[j] in '"_#'):
            j += 1
        diagnostics.append(Diagnostic(
            f"unexpected character(s) {source[i:j]!r}", offsets[i], offsets[j],
            source="lexer"))
        i = j
    index = len(tokens)
    tokens.append(Token(EOF, "", offsets[n], offsets[n], index, False, index))
    return tokens, diagnostics
