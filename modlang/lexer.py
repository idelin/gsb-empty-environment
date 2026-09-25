"""Tokenizer. Every token carries its source position as a UTF-8 byte range."""

from __future__ import annotations

from dataclasses import dataclass

KEYWORDS = {
    "import", "export", "const", "fn", "from", "return",
    "if", "else", "true", "false", "and", "or", "not",
}

_SINGLE_PUNCT = set("{}(),=<>+-*/%!")
_DOUBLE_PUNCT = {"==", "!=", "<=", ">="}


@dataclass(frozen=True)
class Token:
    kind: str   # "ident" | "number" | "string" | "keyword" | "punct" | "eof"
    text: str
    start: int  # UTF-8 byte offset, inclusive
    end: int    # UTF-8 byte offset, exclusive


class LexError(Exception):
    def __init__(self, message: str, start: int, end: int):
        super().__init__(message)
        self.message = message
        self.start = start
        self.end = end


def _width(ch: str) -> int:
    return len(ch.encode("utf-8"))


def tokenize(source: str) -> list[Token]:
    tokens: list[Token] = []
    i = 0
    byte = 0
    n = len(source)

    while i < n:
        ch = source[i]
        if ch in " \t\r\n":
            byte += _width(ch)
            i += 1
            continue
        if ch == "/" and i + 1 < n and source[i + 1] == "/":
            while i < n and source[i] != "\n":
                byte += _width(source[i])
                i += 1
            continue

        start = byte

        if ch.isalpha() or ch == "_":
            j = i
            while j < n and (source[j].isalnum() or source[j] == "_"):
                byte += _width(source[j])
                j += 1
            text = source[i:j]
            kind = "keyword" if text in KEYWORDS else "ident"
            tokens.append(Token(kind, text, start, byte))
            i = j
            continue

        if ch.isdigit():
            j = i
            seen_dot = False
            while j < n and (source[j].isdigit() or (source[j] == "." and not seen_dot)):
                if source[j] == ".":
                    seen_dot = True
                byte += 1
                j += 1
            tokens.append(Token("number", source[i:j], start, byte))
            i = j
            continue

        if ch == '"':
            j = i + 1
            byte += 1
            buf: list[str] = []
            while True:
                if j >= n:
                    raise LexError("unterminated string literal", start, byte)
                c = source[j]
                if c == '"':
                    byte += 1
                    j += 1
                    break
                if c == "\\":
                    nxt = source[j + 1] if j + 1 < n else ""
                    escapes = {"n": "\n", "t": "\t", '"': '"', "\\": "\\"}
                    if nxt not in escapes:
                        raise LexError(f"invalid escape '\\{nxt}'", byte, byte + 2)
                    buf.append(escapes[nxt])
                    byte += _width(c) + _width(nxt)
                    j += 2
                    continue
                buf.append(c)
                byte += _width(c)
                j += 1
            tokens.append(Token("string", "".join(buf), start, byte))
            i = j
            continue

        two = source[i:i + 2]
        if two in _DOUBLE_PUNCT:
            tokens.append(Token("punct", two, start, start + 2))
            byte += 2
            i += 2
            continue
        if ch in _SINGLE_PUNCT:
            tokens.append(Token("punct", ch, start, start + 1))
            byte += 1
            i += 1
            continue

        raise LexError(f"unexpected character {ch!r}", start, start + _width(ch))

    tokens.append(Token("eof", "", byte, byte))
    return tokens
