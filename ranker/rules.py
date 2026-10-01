"""评分规则注册表：分词器与 idf 公式按 ID 版本化。

规则库（RulesStore）只存 ``{version, tokenizer, idf}`` 两个 ID；
具体实现登记在这里。独立验证器只信任本注册表中、且与解释里
``rules_version`` 对应的实现；遇到未知 ID 时只能判“不可验证”。
"""

from __future__ import annotations

import math
from collections.abc import Callable

from .tokenizer import RULES_ID as TOKENIZER_V1
from .tokenizer import tokenize


def _idf_ln_v1(n_docs: int, df: int) -> float:
    """idf = ln((N + 1) / (df + 1)) + 1"""
    return math.log((n_docs + 1.0) / (df + 1.0)) + 1.0


def _idf_bm25_v2(n_docs: int, df: int) -> float:
    """idf = ln(1 + (N - df + 0.5) / (df + 0.5))（BM25 风格，v2 规则）"""
    return math.log(1.0 + (n_docs - df + 0.5) / (df + 0.5))


TOKENIZERS: dict[str, Callable[[str], list[str]]] = {TOKENIZER_V1: tokenize}
IDF_FUNCS: dict[str, Callable[[int, int], float]] = {
    "idf-ln-v1": _idf_ln_v1,
    "idf-bm25-v2": _idf_bm25_v2,
}
