"""分词器。

分词规则（明确、可手算复核）：
1. 文本先整体转小写（仅影响 ASCII 大小写）。
2. 连续的 ASCII 字母或数字（[a-z0-9]+）构成一个词，例如 "bm25"、"apple"。
3. 每一个 CJK 统一表意文字（U+4E00..U+9FFF）单独成词，例如 "苹果" -> ["苹", "果"]。
4. 其余所有字符（空白、标点、emoji 等）都只是分隔符，不产生词。

文档与查询使用同一个分词器。
"""

import re

_TOKEN_RE = re.compile(r"[a-z0-9]+|[\u4e00-\u9fff]")


def tokenize(text):
    """把 UTF-8 文本切成词列表（保留重复出现，顺序与原文一致）。"""
    return _TOKEN_RE.findall(text.lower())
