"""UTF-8 文本分词器。

分词规则（在 README 中同样写明）：
1. 输入按 Unicode 解码，统一转小写（str.casefold）后处理。
2. token 分为两类，按出现顺序切分：
   - 词 (word)：连续的 ASCII 字母/数字，正则 [A-Za-z0-9]+；
   - CJK 字符 (cjk)：单个中日韩统一表意文字
     (U+4E00..U+9FFF 等常见 CJK 区段，见 CJK_RANGES)，
     中文按单字切分，因此“机器学习”->[机,器,学,习]。
3. 空白与标点（含中文标点）一律丢弃，仅作为分隔。
4. 每个 Token 保留：文本、文档内 token 序号、字符起止偏移
   （基于原始大小写fold后的字符串，便于证据回显）。
5. 不做停用词过滤、不做词干化，全部规则确定且无外部依赖。
"""

from __future__ import annotations

import re
from dataclasses import dataclass

CJK_RANGES: tuple[tuple[int, int], ...] = (
    (0x4E00, 0x9FFF),    # CJK 统一表意文字
    (0x3400, 0x4DBF),    # CJK 扩展 A
    (0x20000, 0x2A6DF),  # CJK 扩展 B
    (0xF900, 0xFAFF),    # CJK 兼容表意文字
    (0x3040, 0x30FF),    # 平假名/片假名（日语假名按单字切分）
)

WORD_RE = re.compile(r"[A-Za-z0-9]+")


def _is_cjk(ch: str) -> bool:
    cp = ord(ch)
    return any(lo <= cp <= hi for lo, hi in CJK_RANGES)


@dataclass(frozen=True)
class Token:
    text: str
    pos: int
    start: int
    end: int
    kind: str  # "word" 或 "cjk"


class Tokenizer:
    """规则分词器；同实例可复用，无状态。"""

    def tokenize(self, text: str) -> list[Token]:
        return tokenize(text)


def tokenize(text: str) -> list[Token]:
    text = text.casefold()
    tokens: list[Token] = []
    pos = 0
    i = 0
    n = len(text)
    while i < n:
        m = WORD_RE.match(text, i)
        if m and m.start() == i:
            tokens.append(Token(m.group(), pos, m.start(), m.end(), "word"))
            pos += 1
            i = m.end()
            continue
        ch = text[i]
        if _is_cjk(ch):
            tokens.append(Token(ch, pos, i, i + 1, "cjk"))
            pos += 1
        i += 1
    return tokens


def token_span_text(text: str, start: int, end: int) -> str:
    """按字符偏移取原文片段（用于证据回显）。"""
    return text[start:end]
