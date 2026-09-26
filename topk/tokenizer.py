"""UTF-8 文本分词器（仅标准库）。

分词规则（与 README 一致）：
1. 输入为已解码的 str（UTF-8 文本）；None 视为空文本。
2. 先做一次 Unicode NFKC 规范化：全角字母/数字/符号折叠为半角，
   兼容字符统一为标准形式。
3. 再整体小写化（str.lower），保证大小写不敏感。
4. 切分规则：
   - 连续的 ASCII 字母或数字 [a-z0-9]+ 构成一个 token（英文单词、数字）；
   - 每一个 CJK 表意文字（\\u4e00-\\u9fff）单独成为一个 token（单字 unigram）；
   - 其余字符（空白、中英文标点、符号、emoji 等）一律视为分隔符，直接丢弃。
5. 查询词与文档使用完全相同的分词流程，保证可匹配性。
"""

from __future__ import annotations

import re
import unicodedata

_TOKEN_RE = re.compile(r"[a-z0-9]+|[\u4e00-\u9fff]")


def normalize(text: str | None) -> str:
    """NFKC 规范化 + 小写化；None -> 空串。"""
    if text is None:
        return ""
    return unicodedata.normalize("NFKC", text).lower()


def tokenize(text: str | None) -> list[str]:
    """按上述规则把文本切成 token 列表。"""
    return _TOKEN_RE.findall(normalize(text))


def token_set(text: str | None) -> set[str]:
    """去重后的 token 集合，供过滤条件做成员判断。"""
    return set(tokenize(text))
