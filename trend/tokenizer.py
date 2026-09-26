"""分词器：本地 UTF-8 文本，纯标准库，不依赖搜索服务或全文检索库。

分词规则（明确约定）：
1. 输入为已解码的 Unicode 字符串（UTF-8 文本由调用方解码）。
2. 先做 casefold() 大小写归一（比 lower 更彻底的 Unicode 折叠）。
3. 一个 token 是「Unicode 字母或数字」(str.isalnum() 为真) 的极大连续片段；
   其余一切字符（空白、标点、下划线、符号）都是分隔符。
4. 不做词干化、不去停用词、不做 CJK 切词：连续汉字整体算一个 token，
   例如 "中文文本" 是一个 token，"hello世界" 是一个 token。
5. 文档频率（DF）语义：同一 token 在同一文档中重复出现只计一次，
   由调用方对 token 序列取集合实现。
"""

from __future__ import annotations


def tokenize(text: str) -> list[str]:
    """按上述规则把文本切成 token 列表（保序、可重复）。"""
    tokens: list[str] = []
    buf: list[str] = []
    for ch in text.casefold():
        if ch.isalnum():
            buf.append(ch)
        elif buf:
            tokens.append("".join(buf))
            buf = []
    if buf:
        tokens.append("".join(buf))
    return tokens
