"""分词规则（纯标准库实现，不使用任何搜索服务或全文检索库）。

规则说明：
1. 输入为已解码的 ``str``。本地合成 UTF-8 文本由调用方用
   ``open(path, encoding="utf-8")`` 读入，本模块不做字节级处理。
2. 词条（token）= Unicode 字母/数字的最长连续序列，即对串中每个字符
   判断 ``str.isalnum()``，为 True 的字符归入当前词条，其余字符
   （空白、标点、下划线、emoji 等）一律视为分隔符。
3. 规范化：对每个词条做 ``str.casefold()``（比 lower 更彻底的 Unicode
   小写折叠），索引与查询使用同一规则。
4. 位置（position）：**每个字段内部**从 0 开始编号，每遇到一个词条
   位置 +1。字段之间的位置空间互相独立，因此跨字段不能拼出短语。
5. 每个词条记录其在原文字符串中的字符区间 ``[start, end)``，
   用于把命中位置映射回“匹配原文范围”。
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Token:
    """一个词条：规范化词形、字段内位置、原文字符区间 [start, end)。"""

    term: str
    pos: int
    start: int
    end: int


def tokenize(text: str) -> list[Token]:
    """按模块 docstring 中的规则把 ``text`` 切成词条序列。"""
    tokens: list[Token] = []
    pos = 0
    i = 0
    n = len(text)
    while i < n:
        if text[i].isalnum():
            j = i + 1
            while j < n and text[j].isalnum():
                j += 1
            tokens.append(Token(term=text[i:j].casefold(), pos=pos, start=i, end=j))
            pos += 1
            i = j
        else:
            i += 1
    return tokens
