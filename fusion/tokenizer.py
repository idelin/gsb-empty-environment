"""Deterministic UTF-8 text tokenizer.

公开分词规则（不使用搜索服务或全文检索库）：
1. 输入按 UTF-8 解码（调用方负责以 encoding="utf-8" 读取文件）。
2. Unicode NFKC 规范化后做 casefold，使全角/半角与大小写归一。
3. 连续的拉丁字母/数字序列切为一个词元，例如 "Full-Text2" ->
   ["full", "text2"]；下划线 '_' 视为分隔符。
4. 每个 CJK 统一表意文字（含扩展 A、兼容表意文字）单独切为一个词元。
5. 其余符号（标点、空白、emoji 等）只充当分隔符，不产出词元。
6. 不做停用词过滤、不做词干化；空文本返回空列表。
"""

from __future__ import annotations

import unicodedata

# 0x3000 为 CJK 表意文字起始；覆盖常用汉字范围（基本区 + 扩展 A +
# 兼容表意文字 + 扩展 B~F 与补充区）。逐字符判断，避免引入第三方库。
_CJK_RANGES = (
    (0x3400, 0x4DBF),    # CJK 扩展 A
    (0x4E00, 0x9FFF),    # CJK 统一表意文字
    (0xF900, 0xFAFF),    # CJK 兼容表意文字
    (0x20000, 0x2FFFF),  # 扩展 B 及之后的补充平面
)


def _is_cjk(ch: str) -> bool:
    cp = ord(ch)
    return any(lo <= cp <= hi for lo, hi in _CJK_RANGES)


def _is_alnum(ch: str) -> bool:
    return ch.isalnum() and not _is_cjk(ch)


def tokenize(text: str) -> list[str]:
    if not text:
        return []
    text = unicodedata.normalize("NFKC", text).casefold()
    tokens: list[str] = []
    buf: list[str] = []

    def flush() -> None:
        if buf:
            tokens.append("".join(buf))
            buf.clear()

    for ch in text:
        if _is_alnum(ch):
            buf.append(ch)
        else:
            flush()
            if _is_cjk(ch):
                tokens.append(ch)
    flush()
    return tokens
