"""手写字法分析：把源码切成 token 流，位置记录为 UTF-8 字节区间。"""
from __future__ import annotations

from dataclasses import dataclass

KEYWORDS = {"if", "else", "while", "print"}
OPS2 = {"==", "!=", "<=", ">="}
OPS1 = set("+-*/%(){};=<>")


@dataclass(frozen=True)
class Token:
    kind: str   # 'NUM' | 'ID' | 'KW' | 'OP' | 'EOF'
    text: str
    start: int  # UTF-8 字节偏移（含）
    end: int    # UTF-8 字节偏移（不含）


class LexError(Exception):
    def __init__(self, message: str, start: int, end: int):
        super().__init__(message)
        self.start = start
        self.end = end


def byte_offsets(text: str) -> list[int]:
    """offsets[i] = text 前 i 个字符的 UTF-8 字节数。"""
    offsets = [0] * (len(text) + 1)
    total = 0
    for i, ch in enumerate(text):
        offsets[i] = total
        total += len(ch.encode("utf-8"))
    offsets[len(text)] = total
    return offsets


def tokenize(text: str) -> list[Token]:
    offsets = byte_offsets(text)
    tokens: list[Token] = []
    i, n = 0, len(text)
    while i < n:
        ch = text[i]
        if ch.isspace():
            i += 1
            continue
        if ch == "#":  # 注释到行尾
            while i < n and text[i] != "\n":
                i += 1
            continue
        start = i
        if ch.isdigit():
            while i < n and text[i].isdigit():
                i += 1
            tokens.append(Token("NUM", text[start:i], offsets[start], offsets[i]))
        elif ch.isalpha() or ch == "_":
            while i < n and (text[i].isalnum() or text[i] == "_"):
                i += 1
            word = text[start:i]
            kind = "KW" if word in KEYWORDS else "ID"
            tokens.append(Token(kind, word, offsets[start], offsets[i]))
        elif i + 1 < n and text[i : i + 2] in OPS2:
            tokens.append(Token("OP", text[i : i + 2], offsets[i], offsets[i + 2]))
            i += 2
        elif ch in OPS1:
            tokens.append(Token("OP", ch, offsets[i], offsets[i + 1]))
            i += 1
        else:
            raise LexError(f"非法字符 {ch!r}", offsets[i], offsets[i + 1])
    tokens.append(Token("EOF", "", offsets[n], offsets[n]))
    return tokens
