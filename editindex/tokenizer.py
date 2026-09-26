"""本地 UTF-8 文本的分词与规范化（不依赖任何搜索服务或全文检索库）。

分词规则（与 README 中的说明一致）：

1. 输入是已解码的 ``str``（本地合成 UTF-8 文本）。
2. 先对全文做 NFC 规范化（``unicodedata.normalize("NFC", text)``），
   把可组合的重音序列折叠为预组合码点。
3. **词** = 连续的“词字符”码点组成的最大段。码点 ``ch`` 是词字符当且仅当：
   - ``ch.isalnum()`` 为真（Unicode 类别 L* 字母或 N* 数字），或
   - 类别属于 M*（组合附加符号，如 U+0301，使其依附于前面的字母）。
   其余码点（空白、标点、emoji 符号 So 等）一律视为分隔符。
4. 每个词再做 ``casefold()`` 得到规范形式（如 "STRASSE"→"strasse"）。
5. 词表按首次出现顺序去重：同一规范词只输出一次，顺序稳定。
"""

from __future__ import annotations

import unicodedata


def is_word_char(ch: str) -> bool:
    """判断单个码点是否为词字符（见模块 docstring 第 3 条）。"""
    return ch.isalnum() or unicodedata.category(ch).startswith("M")


def tokenize(text: str) -> list[str]:
    """把文本切分为规范化后的词序列（保持出现顺序，含重复）。"""
    normalized = unicodedata.normalize("NFC", text)
    tokens: list[str] = []
    current: list[str] = []
    for ch in normalized:
        if is_word_char(ch):
            current.append(ch)
        elif current:
            tokens.append("".join(current).casefold())
            current = []
    if current:
        tokens.append("".join(current).casefold())
    return tokens


def normalize_query(query: str) -> str:
    """对查询串应用与分词一致的规范化（NFC + casefold）。"""
    return unicodedata.normalize("NFC", query).casefold()


def vocabulary(tokens: list[str]) -> "list[str]":
    """按首次出现顺序去重，得到稳定顺序的词表。"""
    seen: set[str] = set()
    vocab: list[str] = []
    for token in tokens:
        if token not in seen:
            seen.add(token)
            vocab.append(token)
    return vocab
