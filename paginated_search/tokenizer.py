"""分词规则（明确约定，仅使用标准库，不依赖搜索服务或全文检索库）。

规则如下，按顺序执行：

1. 输入为本地合成的 UTF-8 文本（``str``）。
2. 规范化：先做 Unicode NFKC 规范化，再做 ``str.casefold()`` 大小写折叠。
3. 逐字符扫描，生成词元（token）：
   - 连续的非 CJK 字母数字字符（``str.isalnum()`` 为真且不属于 CJK 区间）
     构成一个整体词元，例如 ``python3``、``café``、``3.11`` 中的 ``3`` 与 ``11``
     （``.`` 是分隔符）。
   - CJK 统一表意文字（U+3400..U+4DBF、U+4E00..U+9FFF）逐字成为单字词元；
     相邻两个 CJK 字额外生成一个二元词元（bigram），例如「分页」产生
     ``分``、``页``、``分页`` 三个词元。
4. 其余所有字符（空白、标点、符号）均为分隔符，不产生词元。

同一套规则同时用于索引文档和解析查询串，保证索引与查询一致。
"""

from __future__ import annotations

import unicodedata

__all__ = ["tokenize"]


def _is_cjk(ch: str) -> bool:
    cp = ord(ch)
    return 0x3400 <= cp <= 0x4DBF or 0x4E00 <= cp <= 0x9FFF


def tokenize(text: str) -> list[str]:
    """按模块 docstring 中的规则把 UTF-8 文本切成词元列表。"""
    normalized = unicodedata.normalize("NFKC", text).casefold()
    tokens: list[str] = []
    run: list[str] = []  # 非 CJK 字母数字的连续片段
    prev_cjk: str | None = None

    def flush_run() -> None:
        if run:
            tokens.append("".join(run))
            run.clear()

    for ch in normalized:
        if _is_cjk(ch):
            flush_run()
            tokens.append(ch)  # 单字词元
            if prev_cjk is not None:
                tokens.append(prev_cjk + ch)  # 二元词元
            prev_cjk = ch
        elif ch.isalnum():
            prev_cjk = None
            run.append(ch)
        else:
            flush_run()
            prev_cjk = None
    flush_run()
    return tokens
