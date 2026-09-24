"""手写字法分析器：token 位置使用 UTF-8 字节区间（start..end，左闭右开）。"""

from dataclasses import dataclass

from .errors import LangError

KEYWORDS = {
    "fn", "let", "if", "elif", "else", "while", "try", "finally",
    "return", "yield", "break", "continue",
    "true", "false", "none", "and", "or", "not",
}

# 多字符运算符必须排在前面
OPS = (
    "==", "!=", "<=", ">=",
    "(", ")", "{", "}", "[", "]", ",", "=",
    "+", "-", "*", "/", "%", "<", ">",
)

_ESCAPES = {"n": "\n", "t": "\t", '"': '"', "\\": "\\"}


@dataclass
class Token:
    kind: str        # 'num' | 'str' | 'name' | 'kw' | 'op' | 'eof'
    text: str
    value: object
    start: int       # UTF-8 字节偏移（含）
    end: int         # UTF-8 字节偏移（不含）


def _byte_offsets(source):
    """每个字符下标对应的 UTF-8 字节偏移表。"""
    offsets = [0] * (len(source) + 1)
    total = 0
    for idx, ch in enumerate(source):
        total += len(ch.encode("utf-8"))
        offsets[idx + 1] = total
    return offsets


def tokenize(source):
    offsets = _byte_offsets(source)
    tokens = []
    i, n = 0, len(source)
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
            text = source[start:i]
            tokens.append(Token("num", text, int(text), offsets[start], offsets[i]))
        elif ch == '"':
            i += 1
            buf = []
            while True:
                if i >= n:
                    raise LangError("unterminated string", offsets[start], offsets[start] + 1)
                c = source[i]
                if c == '"':
                    i += 1
                    break
                if c == "\\":
                    i += 1
                    if i >= n or source[i] not in _ESCAPES:
                        raise LangError("bad escape", offsets[i], offsets[min(i + 1, n)])
                    buf.append(_ESCAPES[source[i]])
                    i += 1
                else:
                    buf.append(c)
                    i += 1
            tokens.append(Token("str", source[start:i], "".join(buf), offsets[start], offsets[i]))
        elif ch.isalpha() or ch == "_":
            while i < n and (source[i].isalnum() or source[i] == "_"):
                i += 1
            text = source[start:i]
            kind = "kw" if text in KEYWORDS else "name"
            tokens.append(Token(kind, text, None, offsets[start], offsets[i]))
        else:
            matched = None
            for op in OPS:
                if source.startswith(op, i):
                    matched = op
                    break
            if matched is None:
                raise LangError(
                    f"unexpected character {ch!r}",
                    offsets[start], offsets[start] + len(ch.encode("utf-8")),
                )
            i += len(matched)
            tokens.append(Token("op", matched, None, offsets[start], offsets[i]))
    tokens.append(Token("eof", "", None, offsets[n], offsets[n]))
    return tokens
