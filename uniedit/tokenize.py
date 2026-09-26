"""本地 UTF-8 文本分词（规则与 README 一致，不依赖任何检索库）。

分词规则：
1. 输入为已解码的 str（UTF-8 文本由调用方 decode）；先做 NFC 规范化，
   使组合序列（如 e + U+0301）与预组合形式（é）得到相同词元。
2. 词元 = Unicode 类别以 L（字母）、M（记号）、N（数字）开头的码点的
   最大连续串。M 类保证组合附加符号跟随基字符留在同一词元内。
3. 其余码点（空白、标点、符号、emoji 等）一律视为分隔符；
   连字符与撇号也分词（"don't" -> "don", "t"），下划线是分隔符。
4. 连续 CJK 表意文字属 L 类，构成一个词元（无空格语言按最大连续
   字母串处理，不做词典切分）。
5. 规范形式 = 词元的 casefold（如 "Straße" -> "strasse"），用于去重；
   输出保留首次出现的原始形式与位置。
"""
from __future__ import annotations

import unicodedata
from dataclasses import dataclass
from typing import List

_WORD_KINDS = ("L", "M", "N")


def _is_word_char(ch: str) -> bool:
    return unicodedata.category(ch)[0] in _WORD_KINDS


@dataclass(frozen=True)
class Token:
    surface: str  # 原始形式（NFC 后）
    norm: str  # 规范形式（casefold）
    position: int  # 在文档中的词元序号（0 起）


def tokenize(text: str) -> List[Token]:
    text = unicodedata.normalize("NFC", text)
    tokens: List[Token] = []
    buf: List[str] = []

    def flush() -> None:
        if buf:
            surface = "".join(buf)
            tokens.append(Token(surface, surface.casefold(), len(tokens)))
            buf.clear()

    for ch in text:
        if _is_word_char(ch):
            buf.append(ch)
        else:
            flush()
    flush()
    return tokens


def normalize_query(query: str) -> str:
    """查询与词元使用同一规范化，保证可比性。"""
    return unicodedata.normalize("NFC", query).casefold()
