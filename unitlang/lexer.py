"""词法分析：直接跟踪 UTF-8 字节偏移，所有 token 都带字节区间。"""
from __future__ import annotations

from dataclasses import dataclass

from .errors import LangError


@dataclass
class Token:
    kind: str  # 'NUM' | 'IDENT' | 'KW' | 'OP' | 'NL' | 'EOF'
    text: str
    start: int  # UTF-8 字节偏移（含）
    end: int    # UTF-8 字节偏移（不含）


KEYWORDS = {"let"}
_TWO_CHAR_OPS = ("->", "==", "!=", "<=", ">=")
_ONE_CHAR_OPS = set("+-*/^()=<>")
_IDENT_EXTRA = {"_", "°", "Δ"}


def _is_ident_start(ch: str) -> bool:
    return ch.isalpha() or ch in _IDENT_EXTRA


def _is_ident_cont(ch: str) -> bool:
    return ch.isalnum() or ch in _IDENT_EXTRA


def tokenize(src: str) -> list[Token]:
    toks: list[Token] = []
    i = 0
    byte = 0
    n = len(src)
    while i < n:
        ch = src[i]
        size = len(ch.encode("utf-8"))
        if ch in " \t\r":
            i += 1
            byte += size
            continue
        if ch == "\n" or ch == ";":
            toks.append(Token("NL", ch, byte, byte + size))
            i += 1
            byte += size
            continue
        if ch == "#":
            while i < n and src[i] != "\n":
                byte += len(src[i].encode("utf-8"))
                i += 1
            continue
        if ch.isdigit() or (ch == "." and i + 1 < n and src[i + 1].isdigit()):
            start_byte = byte
            j = i
            while j < n and src[j].isdigit():
                j += 1
            if j + 1 < n and src[j] == "." and src[j + 1].isdigit():
                j += 1
                while j < n and src[j].isdigit():
                    j += 1
            if j < n and src[j] in "eE":
                k = j + 1
                if k < n and src[k] in "+-":
                    k += 1
                if k < n and src[k].isdigit():
                    while k < n and src[k].isdigit():
                        k += 1
                    j = k
            text = src[i:j]
            byte += len(text)  # 数字字面量只含 ASCII
            toks.append(Token("NUM", text, start_byte, byte))
            i = j
            continue
        if _is_ident_start(ch):
            start_byte = byte
            start_i = i
            while i < n and _is_ident_cont(src[i]):
                byte += len(src[i].encode("utf-8"))
                i += 1
            text = src[start_i:i]
            kind = "KW" if text in KEYWORDS else "IDENT"
            toks.append(Token(kind, text, start_byte, byte))
            continue
        two = src[i : i + 2]
        if two in _TWO_CHAR_OPS:
            toks.append(Token("OP", two, byte, byte + 2))
            byte += 2
            i += 2
            continue
        if ch in _ONE_CHAR_OPS:
            toks.append(Token("OP", ch, byte, byte + size))
            byte += size
            i += 1
            continue
        raise LangError(f"无法识别的字符：{ch!r}", byte, byte + size)
    toks.append(Token("EOF", "", byte, byte))
    return toks
