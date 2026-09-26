"""本地合成 UTF-8 文档上的固定索引版本键集分页查询引擎。"""

from .cursor import (
    CursorError,
    CursorIndexExpiredError,
    CursorQueryMismatchError,
    CursorTamperedError,
)
from .engine import Page, SearchEngine, SearchHit, UpdatePolicy
from .tokenizer import tokenize

__all__ = [
    "CursorError",
    "CursorIndexExpiredError",
    "CursorQueryMismatchError",
    "CursorTamperedError",
    "Page",
    "SearchEngine",
    "SearchHit",
    "UpdatePolicy",
    "tokenize",
]
