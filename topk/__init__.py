"""基于压缩前缀树的 TopK 建议（标准库实现，Python 3.11+）。"""

from .suggester import (
    Cursor,
    Page,
    StaleCursorError,
    Suggestion,
    TopKSuggester,
)
from .tokenizer import normalize, tokenize

__all__ = [
    "Cursor",
    "Page",
    "StaleCursorError",
    "Suggestion",
    "TopKSuggester",
    "normalize",
    "tokenize",
]
