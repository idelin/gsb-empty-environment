"""Hand-written lexer.

Tokens carry ``start``/``end`` offsets measured in UTF-8 *bytes*, not
characters, so spans stay valid for sources containing multibyte text.
"""

from __future__ import annotations

from dataclasses import dataclass

from .errors import LangError

KEYWORDS = {
    "let", "fn", "if", "else", "while", "return", "break", "continue",
    "throw", "try", "catch", "finally", "true", "false", "null",
}

SINGLE_CHAR = {
    "(": "LPAREN",
    ")": "RPAREN",
    "{": "LBRACE",
    "}": "RBRACE",
    ";": "SEMI",
    ",": "COMMA",
    "+": "PLUS",
    "-": "MINUS",
    "*": "STAR",
    "/": "SLASH",
    "%": "PERCENT",
}

DOUBLE_CHAR = {
    "==": "EQ",
    "!=": "NE",
    "<=": "LE",
    ">=": "GE",
    "&&": "AND",
    "||": "OR",
}

SINGLE_OP = {"=": "ASSIGN", "<": "LT", ">": "GT", "!": "BANG"}

ESCAPES = {"n": "\n", "t": "\t", "r": "\r", "\\": "\\", '"': '"', "0": "\0"}


@dataclass
class Token:
    kind: str
    text: str
    value: object
    start: int  # UTF-8 byte offset, inclusive
    end: int    # UTF-8 byte offset, exclusive


def byte_offsets(source: str) -> list[int]:
    """Map each character index to its UTF-8 byte offset."""
    offsets = [0]
    total = 0
    for ch in source:
        total += len(ch.encode("utf-8"))
        offsets.append(total)
    return offsets


def tokenize(source: str) -> list[Token]:
    offsets = byte_offsets(source)
    tokens: list[Token] = []
    i = 0
    n = len(source)

    def emit(kind: str, start: int, end: int, value: object = None) -> None:
        tokens.append(Token(kind, source[start:end], value,
                            offsets[start], offsets[end]))

    while i < n:
        ch = source[i]
        if ch in " \t\r\n":
            i += 1
            continue
        if ch == "/" and i + 1 < n and source[i + 1] == "/":
            while i < n and source[i] != "\n":
                i += 1
            continue
        if ch == "/" and i + 1 < n and source[i + 1] == "*":
            start = i
            i += 2
            while i + 1 < n and source[i:i + 2] != "*/":
                i += 1
            if i + 1 >= n:
                raise LangError("unterminated block comment",
                                (offsets[start], offsets[n]))
            i += 2
            continue

        start = i
        pair = source[i:i + 2]
        if pair in DOUBLE_CHAR:
            emit(DOUBLE_CHAR[pair], i, i + 2)
            i += 2
            continue
        if ch in SINGLE_OP:
            emit(SINGLE_OP[ch], i, i + 1)
            i += 1
            continue
        if ch in SINGLE_CHAR:
            emit(SINGLE_CHAR[ch], i, i + 1)
            i += 1
            continue
        if ch.isdigit():
            while i < n and source[i].isdigit():
                i += 1
            is_float = False
            if i + 1 < n and source[i] == "." and source[i + 1].isdigit():
                is_float = True
                i += 1
                while i < n and source[i].isdigit():
                    i += 1
            text = source[start:i]
            emit("NUMBER", start, i, float(text) if is_float else int(text))
            continue
        if ch == '"':
            i += 1
            chars: list[str] = []
            while True:
                if i >= n:
                    raise LangError("unterminated string",
                                    (offsets[start], offsets[n]))
                c = source[i]
                if c == '"':
                    i += 1
                    break
                if c == "\\":
                    if i + 1 >= n or source[i + 1] not in ESCAPES:
                        raise LangError("bad escape sequence",
                                        (offsets[i], offsets[min(i + 2, n)]))
                    chars.append(ESCAPES[source[i + 1]])
                    i += 2
                else:
                    chars.append(c)
                    i += 1
            emit("STRING", start, i, "".join(chars))
            continue
        if ch.isalpha() or ch == "_":
            while i < n and (source[i].isalnum() or source[i] == "_"):
                i += 1
            text = source[start:i]
            emit(text if text in KEYWORDS else "IDENT", start, i)
            continue
        raise LangError(f"unexpected character {ch!r}",
                        (offsets[i], offsets[i + 1]))

    tokens.append(Token("EOF", "", None, offsets[n], offsets[n]))
    return tokens
