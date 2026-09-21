"""词法分析:把源码切成 token,位置记录为 UTF-8 字节区间。"""
from __future__ import annotations

from typing import NamedTuple

from .ast_nodes import Span
from .errors import ParseError

_SYMBOL_CHARS = set(
    "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_+-*/%=<>!?#$&."
)
_ESCAPES = {"n": "\n", "t": "\t", "r": "\r", "\\": "\\", '"': '"'}


class Token(NamedTuple):
    kind: str  # 'lparen' | 'rparen' | 'symbol' | 'string'
    text: str
    span: Span


def byte_offsets(source: str) -> list[int]:
    """每个字符下标对应的 UTF-8 字节偏移(含末尾哨兵)。"""
    offsets = [0]
    total = 0
    for ch in source:
        total += len(ch.encode("utf-8"))
        offsets.append(total)
    return offsets


def tokenize(source: str) -> list[Token]:
    offsets = byte_offsets(source)
    tokens: list[Token] = []
    i, n = 0, len(source)
    while i < n:
        ch = source[i]
        if ch.isspace():
            i += 1
            continue
        if ch == ";":
            while i < n and source[i] != "\n":
                i += 1
            continue
        if ch == "(":
            tokens.append(Token("lparen", "(", Span(offsets[i], offsets[i + 1])))
            i += 1
            continue
        if ch == ")":
            tokens.append(Token("rparen", ")", Span(offsets[i], offsets[i + 1])))
            i += 1
            continue
        if ch == '"':
            start = i
            i += 1
            buf: list[str] = []
            while i < n and source[i] != '"':
                if source[i] == "\\":
                    i += 1
                    if i >= n or source[i] not in _ESCAPES:
                        raise ParseError(
                            "unknown escape in string",
                            Span(offsets[start], offsets[min(i + 1, n)]),
                        )
                    buf.append(_ESCAPES[source[i]])
                    i += 1
                else:
                    buf.append(source[i])
                    i += 1
            if i >= n:
                raise ParseError(
                    "unterminated string", Span(offsets[start], offsets[n])
                )
            i += 1
            tokens.append(Token("string", "".join(buf), Span(offsets[start], offsets[i])))
            continue
        if ch in _SYMBOL_CHARS:
            start = i
            while i < n and source[i] in _SYMBOL_CHARS:
                i += 1
            tokens.append(Token("symbol", source[start:i], Span(offsets[start], offsets[i])))
            continue
        raise ParseError(
            f"unexpected character {ch!r}", Span(offsets[i], offsets[i + 1])
        )
    return tokens
