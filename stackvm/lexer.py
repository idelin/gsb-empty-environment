"""词法分析。

每个记号携带 start/end, 均为源码的 UTF-8 字节偏移(左闭右开),
因此多字节字符(如中文)的区间也是字节精确的。
"""
from __future__ import annotations

from dataclasses import dataclass


class LexError(Exception):
    pass


@dataclass(frozen=True)
class Token:
    kind: str  # "num" | "ident" | "punct" | "eof"
    text: str
    start: int
    end: int


def tokenize(src: str) -> list[Token]:
    tokens: list[Token] = []
    char_pos = 0       # 字符下标
    byte_pos = 0       # 对应的 UTF-8 字节偏移
    n = len(src)

    def advance() -> None:
        nonlocal char_pos, byte_pos
        ch = src[char_pos]
        char_pos += 1
        byte_pos += len(ch.encode("utf-8"))

    while char_pos < n:
        ch = src[char_pos]
        if ch.isspace():
            advance()
            continue
        if ch == "#":  # 行注释
            while char_pos < n and src[char_pos] != "\n":
                advance()
            continue

        start = byte_pos
        if ch.isdigit():
            j = char_pos
            while char_pos < n and src[char_pos].isdigit():
                advance()
            tokens.append(Token("num", src[j:char_pos], start, byte_pos))
            continue

        if ch.isalpha() or ch == "_":
            j = char_pos
            while char_pos < n and (
                src[char_pos].isalnum() or src[char_pos] == "_"
            ):
                advance()
            tokens.append(
                Token("ident", src[j:char_pos], start, byte_pos)
            )
            continue

        two = src[char_pos : char_pos + 2]
        if two in ("==", "!=", "<=", ">="):
            advance()
            advance()
            tokens.append(Token("punct", two, start, byte_pos))
            continue
        if ch in "+-*/(){};=<>":
            advance()
            tokens.append(Token("punct", ch, start, byte_pos))
            continue

        raise LexError(f"字节 {byte_pos}: 无法识别的字符 {ch!r}")

    tokens.append(Token("eof", "", byte_pos, byte_pos))
    return tokens
