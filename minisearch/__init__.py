"""minisearch: 本地合成 UTF-8 文本上的分段倒排索引与多字段 TopK 归并。

仅使用 Python 3.11 标准库，不依赖任何搜索服务或全文检索库。
"""

from .document import Document
from .segment import IndexSegment
from .index import Index, SearchHit
from .sort import SortField, NULLS_FIRST, NULLS_LAST
from .tokenizer import tokenize, SCORE_FIELD

__all__ = [
    "Document",
    "IndexSegment",
    "Index",
    "SearchHit",
    "SortField",
    "NULLS_FIRST",
    "NULLS_LAST",
    "tokenize",
    "SCORE_FIELD",
]
