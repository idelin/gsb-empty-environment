"""分词规则（本文件即权威定义，README 同步说明）。

规则：
1. 输入为 UTF-8 解码后的 str；先做 NFKC 规范化，再 casefold（大小写折叠）。
2. CJK 表意文字（中日韩统一表意文字区间）逐字成词（unigram），
   因为中文等文本没有空白分词。
3. 其余 Unicode 字母/数字（str.isalnum() 为真）的极大连续段构成一个词。
4. 其他字符（空白、标点、符号）一律视为分隔符，不产生词。
5. 位置（position）按生成的词序从 0 递增，供短语查询使用。
"""

import unicodedata

_CJK_RANGES = (
    (0x4E00, 0x9FFF),    # CJK Unified Ideographs
    (0x3400, 0x4DBF),    # CJK Extension A
    (0xF900, 0xFAFF),    # CJK Compatibility Ideographs
)


def _is_cjk(ch: str) -> bool:
    cp = ord(ch)
    return any(lo <= cp <= hi for lo, hi in _CJK_RANGES)


def tokenize(text: str) -> list[str]:
    """按模块 docstring 中的规则把文本切成词序列。"""
    normalized = unicodedata.normalize("NFKC", text).casefold()
    tokens: list[str] = []
    buf: list[str] = []
    for ch in normalized:
        if _is_cjk(ch):
            if buf:
                tokens.append("".join(buf))
                buf = []
            tokens.append(ch)
        elif ch.isalnum():
            buf.append(ch)
        else:
            if buf:
                tokens.append("".join(buf))
                buf = []
    if buf:
        tokens.append("".join(buf))
    return tokens
