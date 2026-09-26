"""本地 UTF-8 文本分词器（不依赖任何搜索服务或全文检索库）。

分词规则（务必与 README 保持一致）：

1. 输入文本先做 Unicode NFC 规范化（``unicodedata.normalize("NFC", text)``），
   使组合字符与预组合字符等价，例如 "é" 的两种编码视为同一字符。
2. 一个词（token）是满足以下条件的字符的**最大连续序列**：
   ``ch == "_"`` 或 ``ch.isalnum()`` 为真（Unicode 字母、数字，含 CJK 表意文字）。
   其余所有字符（空白、标点、连字符、撇号等）都是分隔符。
   例如 "state-of-the-art" 会被切成 "state" / "of" / "the" / "art"，
   "don't" 会被切成 "don" / "t"。
3. 分词器保留词的**原始表面形式**（display form），不做大小写折叠。
   大小写规范化（``str.casefold``）在索引层完成，用于聚合身份；
   显示形式由索引层按别名观测频次选出。
"""

from __future__ import annotations

import unicodedata
from typing import Iterator

__all__ = ["normalize", "is_word_char", "tokenize"]


def normalize(text: str) -> str:
    """词的规范身份：NFC + casefold。用于聚合大小写/重音编码别名。"""
    return unicodedata.normalize("NFC", text).casefold()


def is_word_char(ch: str) -> bool:
    return ch == "_" or ch.isalnum()


def tokenize(text: str) -> Iterator[str]:
    """按上述规则产出词的表面形式序列。"""
    text = unicodedata.normalize("NFC", text)
    current: list[str] = []
    for ch in text:
        if is_word_char(ch):
            current.append(ch)
        elif current:
            yield "".join(current)
            current.clear()
    if current:
        yield "".join(current)
