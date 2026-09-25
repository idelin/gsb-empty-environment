"""词法分析器：把源码切成词法单元，位置一律使用 UTF-8 字节区间。"""

from dataclasses import dataclass

from .errors import LangError

KEYWORDS = frozenset([
    "def", "let", "in", "if", "then", "else", "try", "finally",
    "and", "or", "not", "true", "false",
])

_TWO_CHAR_OPS = ("==", "!=", "<=", ">=")
_ONE_CHAR_OPS = "+-*/%(),=<>"
_ESCAPES = {"n": "\n", "t": "\t", '"': '"', "\\": "\\"}


@dataclass
class Token:
    kind: str   # INT / STR / NAME / KW / OP / EOF
    text: str
    start: int  # UTF-8 字节偏移（含）
    end: int    # UTF-8 字节偏移（不含）


def _byte_offsets(source):
    """offsets[i] 是第 i 个字符的 UTF-8 字节偏移。"""
    offsets = [0]
    total = 0
    for ch in source:
        total += len(ch.encode("utf-8"))
        offsets.append(total)
    return offsets


def tokenize(source):
    offsets = _byte_offsets(source)
    tokens = []
    i = 0
    n = len(source)
    while i < n:
        ch = source[i]
        if ch.isspace():
            i += 1
            continue
        if ch == "#":
            while i < n and source[i] != "\n":
                i += 1
            continue
        start = i
        if ch.isdigit():
            while i < n and source[i].isdigit():
                i += 1
            tokens.append(Token("INT", source[start:i], offsets[start], offsets[i]))
            continue
        if ch.isalpha() or ch == "_":
            while i < n and (source[i].isalnum() or source[i] == "_"):
                i += 1
            text = source[start:i]
            kind = "KW" if text in KEYWORDS else "NAME"
            tokens.append(Token(kind, text, offsets[start], offsets[i]))
            continue
        if ch == '"':
            i += 1
            buf = []
            while True:
                if i >= n:
                    raise LangError("字符串未闭合", (offsets[start], offsets[n]))
                c = source[i]
                if c == '"':
                    i += 1
                    break
                if c == "\\" and i + 1 < n:
                    buf.append(_ESCAPES.get(source[i + 1], source[i + 1]))
                    i += 2
                else:
                    buf.append(c)
                    i += 1
            tokens.append(Token("STR", "".join(buf), offsets[start], offsets[i]))
            continue
        two = source[i:i + 2]
        if two in _TWO_CHAR_OPS:
            tokens.append(Token("OP", two, offsets[i], offsets[i + 2]))
            i += 2
            continue
        if ch in _ONE_CHAR_OPS:
            tokens.append(Token("OP", ch, offsets[i], offsets[i + 1]))
            i += 1
            continue
        raise LangError("无法识别的字符：{!r}".format(ch), (offsets[i], offsets[i + 1]))
    tokens.append(Token("EOF", "", offsets[n], offsets[n]))
    return tokens
