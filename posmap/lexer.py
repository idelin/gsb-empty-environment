"""UTF-8 字节偏移词法器。

token 区间一律是 UTF-8 字节区间（非字符偏移），非 ASCII 字节视为标识符
组成部分，因此中文标识符/注释可直接使用且字节位置保持正确。
"""
from __future__ import annotations

from dataclasses import dataclass


KEYWORDS = {"let", "print"}
PUNCTUATORS = set("+-*/()=;,")


class LexError(Exception):
    def __init__(self, message: str, offset: int):
        super().__init__(f"{message} @ byte {offset}")
        self.offset = offset


@dataclass(frozen=True)
class Token:
    kind: str   # IDENT / INT / PUNCT / KEYWORD
    text: str
    start: int
    end: int


def _is_ident_start(c: int) -> bool:
    return c == ord("_") or 65 <= c <= 90 or 97 <= c <= 122 or c >= 128


def _is_ident_part(c: int) -> bool:
    return _is_ident_start(c) or 48 <= c <= 57


def lex(text: str):
    data = text.encode("utf-8")
    tokens = []
    i = 0
    n = len(data)
    while i < n:
        c = data[i]
        if c in (ord(" "), ord("\t"), ord("\r"), ord("\n")):
            i += 1
            continue
        if c == ord("#"):
            while i < n and data[i] != ord("\n"):
                i += 1
            continue
        start = i
        if 48 <= c <= 57:
            while i < n and 48 <= data[i] <= 57:
                i += 1
            tokens.append(Token("INT", data[start:i].decode("utf-8"), start, i))
        elif _is_ident_start(c):
            while i < n and _is_ident_part(data[i]):
                i += 1
            word = data[start:i].decode("utf-8")
            kind = "KEYWORD" if word in KEYWORDS else "IDENT"
            tokens.append(Token(kind, word, start, i))
        elif chr(c) in PUNCTUATORS:
            tokens.append(Token("PUNCT", chr(c), start, start + 1))
            i += 1
        else:
            raise LexError(f"无法识别的字节 {c:#x}", start)
    return tokens
