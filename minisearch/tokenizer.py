"""分词器（仅标准库 re/unicodedata）。

分词规则（在文档中显式声明，便于复现）：
1. 输入必须是 ``str``（本地 UTF-8 文本经 UTF-8 解码后得到）；字节串不隐式解码。
2. 先做 Unicode NFKC 规范化（全角／兼容字符归一），再做 ``casefold``（大小写归一，
   比 ``lower`` 更彻底，例如 ß -> ss）。
3. 下划线视为分隔符（不作为词字符，``\\w`` 会包含下划线，因此先替换为空格）。
4. 用正则 ``[^\\W_]+`` 切出“字母/数字连续段”。
5. 段内再拆分：
   - CJK 表意文字及假名（U+3040..U+30FF、U+3400..U+4DBF、U+4E00..U+9FFF、
     U+F900..U+FAFF）逐字输出单字 token；
   - 其余非 CJK 字符连续合并为一个 token（拉丁字母、数字、阿拉伯文等）。

示例：
    "Wi-Fi 机械 keyboard_42 ßtraße" ->
    ["wi", "fi", "机", "械", "keyboard", "42", "sstrasse"]
"""

from __future__ import annotations

import re
import unicodedata

SCORE_FIELD = "_score"

_WORD_RUN_RE = re.compile(r"[^\W_]+", re.UNICODE)
_CJK_RANGES = (
    (0x3040, 0x30FF),  # 平假名、片假名
    (0x3400, 0x4DBF),  # CJK 扩展 A
    (0x4E00, 0x9FFF),  # CJK 统一表意文字
    (0xF900, 0xFAFF),  # CJK 兼容表意文字
)


def _is_cjk(char: str) -> bool:
    code = ord(char)
    return any(start <= code <= end for start, end in _CJK_RANGES)


def tokenize(text: str) -> list[str]:
    """按模块文档字符串中的规则把文本切成 term 列表。"""
    if not isinstance(text, str):
        raise TypeError(f"tokenize 需要 str（UTF-8 解码后的文本），收到 {type(text)!r}")
    normalized = unicodedata.normalize("NFKC", text).casefold()
    tokens: list[str] = []
    for run in _WORD_RUN_RE.findall(normalized.replace("_", " ")):
        pending: list[str] = []
        for char in run:
            if _is_cjk(char):
                if pending:
                    tokens.append("".join(pending))
                    pending = []
                tokens.append(char)
            else:
                pending.append(char)
        if pending:
            tokens.append("".join(pending))
    return tokens
