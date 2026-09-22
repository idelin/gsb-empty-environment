"""Hand-written lexer. Token spans are UTF-8 byte offsets into the source."""

from __future__ import annotations

from dataclasses import dataclass

from .errors import LexError


@dataclass
class Token:
    kind: str  # NUMBER, IDENT, OP, NEWLINE, EOF
    text: str
    span: tuple[int, int]


_OPERATORS = ("==", "!=", "<=", ">=", "+", "-", "*", "/", "(", ")", "=", "<", ">")
_IDENT_EXTRA = "°Δ_"


def tokenize(source: str) -> list[Token]:
    tokens: list[Token] = []
    i = 0
    byte = 0
    n = len(source)
    while i < n:
        ch = source[i]
        if ch in " \t\r":
            i += 1
            byte += 1
            continue
        if ch == "#":
            while i < n and source[i] != "\n":
                byte += len(source[i].encode("utf-8"))
                i += 1
            continue
        if ch == "\n":
            tokens.append(Token("NEWLINE", "\n", (byte, byte + 1)))
            i += 1
            byte += 1
            continue
        start = byte
        if ch.isdigit() or (ch == "." and i + 1 < n and source[i + 1].isdigit()):
            j = i
            while j < n and source[j].isdigit():
                j += 1
            if j < n and source[j] == ".":
                j += 1
                while j < n and source[j].isdigit():
                    j += 1
            if j < n and source[j] in "eE":
                k = j + 1
                if k < n and source[k] in "+-":
                    k += 1
                if k < n and source[k].isdigit():
                    j = k
                    while j < n and source[j].isdigit():
                        j += 1
            text = source[i:j]
            byte += len(text.encode("utf-8"))
            tokens.append(Token("NUMBER", text, (start, byte)))
            i = j
            continue
        if ch.isalpha() or ch in _IDENT_EXTRA:
            j = i
            while j < n and (source[j].isalnum() or source[j] in _IDENT_EXTRA):
                j += 1
            text = source[i:j]
            byte += len(text.encode("utf-8"))
            tokens.append(Token("IDENT", text, (start, byte)))
            i = j
            continue
        matched = None
        for op in _OPERATORS:
            if source.startswith(op, i):
                matched = op
                break
        if matched is not None:
            tokens.append(Token("OP", matched, (start, start + len(matched))))
            i += len(matched)
            byte += len(matched)
            continue
        width = len(ch.encode("utf-8"))
        raise LexError(f"unexpected character {ch!r}", (start, start + width))
    tokens.append(Token("EOF", "", (byte, byte)))
    return tokens
