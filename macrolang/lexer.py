"""词法分析：把源码切成带 UTF-8 字节区间的 token。

语法约定：
  - 空白分隔；`;` 到行尾为注释；
  - `(` `)` 为结构符；
  - 整数如 `42`、`-7`；
  - 字符串用双引号，支持 \\n \\t \\" \\\\ 转义；
  - 其余连续非分隔字符为符号；`#` 保留给卫生重命名，用户源码禁用。
"""
from __future__ import annotations

import re
from dataclasses import dataclass

from .errors import LexError
from .nodes import Span

_INT_RE = re.compile(r"-?\d+")
_DELIMS = set('()";\t\r\n ')
_ESCAPES = {"n": "\n", "t": "\t", '"': '"', "\\": "\\"}


def byte_offsets(source: str) -> list[int]:
    """字符下标 -> UTF-8 字节偏移 的映射表。"""
    offsets = [0] * (len(source) + 1)
    total = 0
    for index, ch in enumerate(source):
        offsets[index] = total
        total += len(ch.encode("utf-8"))
    offsets[len(source)] = total
    return offsets


@dataclass
class Token:
    kind: str  # 'lparen' | 'rparen' | 'int' | 'string' | 'sym'
    text: str
    span: Span


def lex(source: str) -> list[Token]:
    offsets = byte_offsets(source)
    tokens: list[Token] = []
    index = 0
    length = len(source)

    def make_span(start: int, end: int) -> Span:
        return Span(offsets[start], offsets[end])

    while index < length:
        ch = source[index]
        if ch in " \t\r\n":
            index += 1
            continue
        if ch == ";":
            while index < length and source[index] != "\n":
                index += 1
            continue
        if ch == "(":
            tokens.append(Token("lparen", ch, make_span(index, index + 1)))
            index += 1
            continue
        if ch == ")":
            tokens.append(Token("rparen", ch, make_span(index, index + 1)))
            index += 1
            continue
        if ch == '"':
            start = index
            index += 1
            buf: list[str] = []
            while True:
                if index >= length:
                    raise LexError(
                        "unterminated string literal", make_span(start, length)
                    )
                cur = source[index]
                if cur == "\\":
                    if index + 1 >= length:
                        raise LexError(
                            "unterminated escape sequence",
                            make_span(start, length),
                        )
                    esc = source[index + 1]
                    if esc not in _ESCAPES:
                        raise LexError(
                            f"unknown escape \\{esc}",
                            make_span(index, index + 2),
                        )
                    buf.append(_ESCAPES[esc])
                    index += 2
                    continue
                if cur == '"':
                    index += 1
                    break
                buf.append(cur)
                index += 1
            tokens.append(Token("string", "".join(buf), make_span(start, index)))
            continue
        if ch == "#":
            raise LexError(
                "'#' is reserved for hygienic renaming",
                make_span(index, index + 1),
            )
        start = index
        while index < length and source[index] not in _DELIMS and source[index] != "#":
            index += 1
        text = source[start:index]
        kind = "int" if _INT_RE.fullmatch(text) else "sym"
        tokens.append(Token(kind, text, make_span(start, index)))
    return tokens
