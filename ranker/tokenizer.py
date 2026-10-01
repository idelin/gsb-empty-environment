"""分词规则（写清、版本化，供主检索器与独立验证器共用）。

RULES_ID = "unicode-unigram-v1"：

1. 输入为 UTF-8 解码后的 ``str``。
2. 先做 Unicode NFC 规范化，再整体 ``lower()``（大小写不敏感）。
3. 从左到右逐字符扫描：
   - ASCII 字母或数字（``[a-z0-9]``）累积进当前 token；
   - 其余 Unicode 字母数字字符（``str.isalnum()`` 为真，如 CJK 汉字）
     各自成为**单字 token**（unigram），并冲刷当前累积中的 ASCII 串；
   - 其它字符（空白、标点、符号等）一律视为分隔符，冲刷当前累积。
4. 输出保序 token 列表，重复保留（用于计算 tf）。

例：``"Python3.11 机器学习!"`` -> ``["python3", "11", "机", "器", "学", "习"]``
"""

from __future__ import annotations

import unicodedata

RULES_ID = "unicode-unigram-v1"


def tokenize(text: str) -> list[str]:
    text = unicodedata.normalize("NFC", text).lower()
    tokens: list[str] = []
    buf: list[str] = []
    for ch in text:
        if ch.isascii() and ch.isalnum():
            buf.append(ch)
            continue
        if buf:
            tokens.append("".join(buf))
            buf.clear()
        if ch.isalnum():
            tokens.append(ch)
    if buf:
        tokens.append("".join(buf))
    return tokens
