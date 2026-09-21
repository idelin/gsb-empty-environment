"""词法分析:把源码切成 token,所有位置均为 UTF-8 字节区间。"""

from dataclasses import dataclass

KEYWORDS = {"def": "DEF", "let": "LET"}
PUNCT = {
    "(": "LPAREN",
    ")": "RPAREN",
    ",": "COMMA",
    "=": "EQ",
    ";": "SEMI",
    "+": "PLUS",
    "-": "MINUS",
    "*": "STAR",
    "/": "SLASH",
}


@dataclass(frozen=True)
class Token:
    kind: str
    text: str
    start: int  # 起始字节偏移(含)
    end: int    # 结束字节偏移(不含)


def _byte_offsets(text):
    """字符下标 -> 字节偏移 的映射表。"""
    offsets = [0] * (len(text) + 1)
    total = 0
    for i, ch in enumerate(text):
        offsets[i] = total
        total += len(ch.encode("utf-8"))
    offsets[len(text)] = total
    return offsets


def lex(text):
    """返回 (tokens, errors);errors 为 (message, start, end) 字节区间。"""
    offsets = _byte_offsets(text)
    tokens = []
    errors = []
    i = 0
    n = len(text)
    while i < n:
        ch = text[i]
        if ch.isspace():
            i += 1
            continue
        if ch == "#":  # 行注释
            while i < n and text[i] != "\n":
                i += 1
            continue
        start = i
        if ch.isdigit() or (ch == "." and i + 1 < n and text[i + 1].isdigit()):
            i += 1
            while i < n and text[i].isdigit():
                i += 1
            if i < n and text[i] == ".":
                i += 1
                while i < n and text[i].isdigit():
                    i += 1
            tokens.append(Token("NUMBER", text[start:i], offsets[start], offsets[i]))
            continue
        if ch.isalpha() or ch == "_":
            i += 1
            while i < n and (text[i].isalnum() or text[i] == "_"):
                i += 1
            word = text[start:i]
            tokens.append(Token(KEYWORDS.get(word, "IDENT"), word,
                                offsets[start], offsets[i]))
            continue
        if ch in PUNCT:
            i += 1
            tokens.append(Token(PUNCT[ch], ch, offsets[start], offsets[i]))
            continue
        i += 1
        tokens.append(Token("BAD", ch, offsets[start], offsets[i]))
        errors.append((f"无法识别的字符 {ch!r}", offsets[start], offsets[i]))
    tokens.append(Token("EOF", "", offsets[n], offsets[n]))
    return tokens, errors
