"""分词器：本地合成 UTF-8 文本的分词规则实现。

分词规则（明确约定，不依赖任何搜索服务或全文检索库）：

1. 输入为已解码的 UTF-8 字符串（str）。
2. ASCII 字母与数字（[A-Za-z0-9]）的连续序列构成一个词元（token），
   统一转为小写。例如 "Python3.11" -> ["python3", "11"]。
3. 每个 CJK 表意文字（中日韩统一表意文字基本区 U+4E00-U+9FFF、
   扩展 A 区 U+3400-U+4DBF、兼容表意文字 U+F900-U+FAFF）
   单独成为一个单字词元。例如 "水果" -> ["水", "果"]。
4. 其余所有字符（空白、标点、符号等）均为分隔符，不产生词元。

查询时使用同一分词器；查询串含多个词元时按 AND 语义求交集。
"""


def _is_cjk(ch: str) -> bool:
    cp = ord(ch)
    return (
        0x4E00 <= cp <= 0x9FFF
        or 0x3400 <= cp <= 0x4DBF
        or 0xF900 <= cp <= 0xFAFF
    )


def tokenize(text: str) -> list[str]:
    tokens: list[str] = []
    buf: list[str] = []
    for ch in text:
        if ch.isascii() and ch.isalnum():
            buf.append(ch.lower())
        elif _is_cjk(ch):
            if buf:
                tokens.append("".join(buf))
                buf = []
            tokens.append(ch)
        else:
            if buf:
                tokens.append("".join(buf))
                buf = []
    if buf:
        tokens.append("".join(buf))
    return tokens
