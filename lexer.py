"""手写词法分析器。token 的 span 是 UTF-8 字节区间 [start, end)。"""
from __future__ import annotations

from dataclasses import dataclass

KEYWORDS = {"let", "if", "else", "print", "true", "false", "and", "or", "not"}
MULTI_OPS = ("<=", ">=", "==", "!=")
SINGLE_CHARS = set("+-*/%(){};=<>")


class LexError(Exception):
    def __init__(self, message, span):
        super().__init__(message)
        self.message = message
        self.span = span


@dataclass
class Token:
    kind: str   # 'INT' | 'IDENT' | 'KW' | 'OP' | 'EOF'
    text: str
    span: tuple


def byte_offset_table(text):
    """table[i] = text 中第 i 个字符（code point）起始处的 UTF-8 字节偏移。"""
    table = [0] * (len(text) + 1)
    total = 0
    for i, ch in enumerate(text):
        table[i] = total
        total += len(ch.encode("utf-8"))
    table[len(text)] = total
    return table


def tokenize(text):
    bt = byte_offset_table(text)
    toks = []
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
            toks.append(Token("INT", text[start:i], (bt[start], bt[i])))
            continue
        if ch.isalpha() or ch == "_" or ord(ch) > 127:
            while i < n and (text[i].isalnum() or text[i] == "_" or ord(text[i]) > 127):
                i += 1
            word = text[start:i]
            kind = "KW" if word in KEYWORDS else "IDENT"
            toks.append(Token(kind, word, (bt[start], bt[i])))
            continue
        two = text[i:i + 2]
        if two in MULTI_OPS:
            toks.append(Token("OP", two, (bt[i], bt[i + 2])))
            i += 2
            continue
        if ch in SINGLE_CHARS:
            toks.append(Token("OP", ch, (bt[i], bt[i + 1])))
            i += 1
            continue
        raise LexError("unexpected character %r" % ch, (bt[i], bt[i + 1]))
    toks.append(Token("EOF", "", (bt[n], bt[n])))
    return toks
