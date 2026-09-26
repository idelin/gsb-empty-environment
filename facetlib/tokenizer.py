"""UTF-8 文本分词（仅标准库，规则确定、可复现，不使用任何全文检索库）。

分词规则
--------
1. 文本先做 Unicode NFKC 规范化，再做 casefold（大小写折叠）；
2. ASCII 字母 / 数字 / 下划线的最长连续段构成一个词元（word token）；
3. 每个 CJK 表意字符（U+3400..U+4DBF、U+4E00..U+9FFF、U+F900..U+FAFF 区间）
   单独成为一个单字词元（unigram），不跨字组词；
4. 其余所有字符（空白、标点、符号、其他文字）一律视为分隔符，不产生词元。

示例：``"Hello, 世界! foo_bar2 ＡＢ"`` -> ``["hello", "世", "界", "foo_bar2", "ab"]``
"""

from __future__ import annotations

import unicodedata

_CJK_RANGES = (
    (0x3400, 0x4DBF),
    (0x4E00, 0x9FFF),
    (0xF900, 0xFAFF),
)


def _is_cjk(ch: str) -> bool:
    cp = ord(ch)
    return any(lo <= cp <= hi for lo, hi in _CJK_RANGES)


def _is_word_char(ch: str) -> bool:
    return ch.isascii() and (ch.isalnum() or ch == "_")


def tokenize(text: str) -> list[str]:
    """按模块 docstring 中的规则把 UTF-8 文本切分为词元列表。"""
    text = unicodedata.normalize("NFKC", text).casefold()
    tokens: list[str] = []
    buf: list[str] = []
    for ch in text:
        if _is_word_char(ch):
            buf.append(ch)
        else:
            if buf:
                tokens.append("".join(buf))
                buf.clear()
            if _is_cjk(ch):
                tokens.append(ch)
    if buf:
        tokens.append("".join(buf))
    return tokens
