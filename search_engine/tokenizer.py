"""分词器：纯标准库实现，不依赖任何搜索服务或全文检索库。

分词规则（查询与文档使用完全相同的规则，保证一致性）：

1. 输入为 UTF-8 解码后的 ``str``。
2. 先对全文做 Unicode 大小写折叠（``str.casefold()``），
   使 ``Apple`` 与 ``apple`` 视为同一词。
3. CJK 字符（中日韩统一表意文字、扩展 A 区、平假名、片假名、谚文音节）
   逐字独立成词，因为标准库没有中文分词能力。
4. 其余连续的字母或数字（``str.isalnum()`` 为真）合并为一个词，
   例如 ``python3.11`` 会被切成 ``python3`` 和 ``11`` 两个词。
5. 空白、标点及一切其他字符均为分隔符，自身不产生词。
"""

from __future__ import annotations


def _is_cjk(char: str) -> bool:
    code = ord(char)
    return (
        0x4E00 <= code <= 0x9FFF      # CJK 统一表意文字
        or 0x3400 <= code <= 0x4DBF   # 扩展 A 区
        or 0x3040 <= code <= 0x309F   # 平假名
        or 0x30A0 <= code <= 0x30FF   # 片假名
        or 0xAC00 <= code <= 0xD7AF   # 谚文音节
    )


def tokenize(text: str) -> list[str]:
    """按模块 docstring 中的规则把文本切分为词项列表。"""
    tokens: list[str] = []
    buffer: list[str] = []
    for char in text.casefold():
        if _is_cjk(char):
            if buffer:
                tokens.append("".join(buffer))
                buffer.clear()
            tokens.append(char)
        elif char.isalnum():
            buffer.append(char)
        else:
            if buffer:
                tokens.append("".join(buffer))
                buffer.clear()
    if buffer:
        tokens.append("".join(buffer))
    return tokens
