"""固定索引版本 + 排序键分页的最小检索引擎（仅标准库）。"""

from .engine import (
    CursorError,
    CursorInvalidated,
    CursorQueryMismatch,
    Page,
    SearchEngine,
    SearchHit,
)
from .tokenizer import tokenize

__all__ = [
    "CursorError",
    "CursorInvalidated",
    "CursorQueryMismatch",
    "Page",
    "SearchEngine",
    "SearchHit",
    "tokenize",
]
