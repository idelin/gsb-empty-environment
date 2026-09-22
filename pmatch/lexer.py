"""Hand-written lexer. Token spans are UTF-8 byte ranges."""
from __future__ import annotations

from dataclasses import dataclass


class PError(Exception):
    """Lex/parse error carrying a byte span."""

    def __init__(self, message, span):
        super().__init__(message)
        self.message = message
        self.span = span


KEYWORDS = {"union", "match", "if", "and", "or", "not", "true", "false", "none"}
RESERVED = {"union", "match", "if", "and", "or", "not"}

_TWO_CHAR_OPS = ("->", "==", "!=", "<=", ">=")
_ONE_CHAR_OPS = "(),:|+-*/%<>="

_ESCAPES = {"n": "\n", "t": "\t", "r": "\r", "\\": "\\", '"': '"', "'": "'"}


@dataclass
class Token:
    kind: str      # "INT" | "STRING" | "NAME" | "OP" | "NEWLINE" | "EOF"
    text: str
    value: object  # parsed value for INT / STRING
    span: tuple    # (start, end) UTF-8 byte offsets


def byte_offsets(src):
    """offsets[i] = UTF-8 byte offset of the i-th character of ``src``."""
    offsets = [0]
    for ch in src:
        offsets.append(offsets[-1] + len(ch.encode("utf-8")))
    return offsets


def lex(src):
    offsets = byte_offsets(src)
    toks = []
    i, n = 0, len(src)

    def span(a, b):
        return (offsets[a], offsets[b])

    while i < n:
        ch = src[i]
        if ch in " \t\r":
            i += 1
            continue
        if ch == "#":
            while i < n and src[i] != "\n":
                i += 1
            continue
        if ch == "\n":
            toks.append(Token("NEWLINE", "\n", None, span(i, i + 1)))
            i += 1
            continue
        if ch.isdigit():
            j = i
            while j < n and src[j].isdigit():
                j += 1
            toks.append(Token("INT", src[i:j], int(src[i:j]), span(i, j)))
            i = j
            continue
        if ch in "\"'":
            quote = ch
            j = i + 1
            buf = []
            while j < n and src[j] != quote and src[j] != "\n":
                if src[j] == "\\" and j + 1 < n and src[j + 1] in _ESCAPES:
                    buf.append(_ESCAPES[src[j + 1]])
                    j += 2
                else:
                    buf.append(src[j])
                    j += 1
            if j >= n or src[j] != quote:
                raise PError("unterminated string literal", span(i, j))
            j += 1
            toks.append(Token("STRING", src[i:j], "".join(buf), span(i, j)))
            i = j
            continue
        if ch.isalpha() or ch == "_":
            j = i
            while j < n and (src[j].isalnum() or src[j] == "_"):
                j += 1
            toks.append(Token("NAME", src[i:j], None, span(i, j)))
            i = j
            continue
        two = src[i:i + 2]
        if two in _TWO_CHAR_OPS:
            toks.append(Token("OP", two, None, span(i, i + 2)))
            i += 2
            continue
        if ch in _ONE_CHAR_OPS:
            toks.append(Token("OP", ch, None, span(i, i + 1)))
            i += 1
            continue
        raise PError(f"unexpected character {ch!r}", span(i, i + 1))
    toks.append(Token("EOF", "", None, span(n, n)))
    return toks
