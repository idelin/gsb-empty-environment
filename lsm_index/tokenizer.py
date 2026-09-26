"""Tokenizer for locally synthesized UTF-8 text.

分词规则（索引时与查询时完全一致）:
1. 输入若为 bytes，使用 UTF-8 严格解码（errors="strict"），非法字节抛 UnicodeDecodeError。
2. 统一用 str.casefold() 做 Unicode 大小写折叠（例如 'ß' -> 'ss'）。
3. CJK 表意文字（U+4E00-U+9FFF、U+3400-U+4DBF）按字切分，一个汉字一个 token。
4. 其余连续的 isalnum() 字符（拉丁字母、数字等）组成一个 token，
   非字母数字字符（标点、空白等）一律作为分隔符，不产生 token。
5. 不去停用词、不做词干提取；分词是确定性纯函数。
"""

from __future__ import annotations

_CJK_RANGES = (
    (0x4E00, 0x9FFF),
    (0x3400, 0x4DBF),
)


def _is_cjk(ch: str) -> bool:
    cp = ord(ch)
    return any(lo <= cp <= hi for lo, hi in _CJK_RANGES)


def tokenize(text: str | bytes) -> list[str]:
    if isinstance(text, bytes):
        text = text.decode("utf-8", errors="strict")
    tokens: list[str] = []
    run: list[str] = []

    def flush() -> None:
        if run:
            tokens.append("".join(run))
            run.clear()

    for ch in text.casefold():
        if _is_cjk(ch):
            flush()
            tokens.append(ch)
        elif ch.isalnum():
            run.append(ch)
        else:
            flush()
    flush()
    return tokens
