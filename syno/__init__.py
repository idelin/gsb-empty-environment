"""本地同义词扩展检索原型（标准库实现，无搜索服务/全文检索库）。"""

from .tokenizer import Token, tokenize, Tokenizer
from .synonyms import SynonymStore
from .query_graph import QueryExpander, QueryGraph
from .index import Document, PositionalIndex
from .search import Searcher, DocResult, Evidence

__all__ = [
    "Token",
    "tokenize",
    "Tokenizer",
    "SynonymStore",
    "QueryExpander",
    "QueryGraph",
    "Document",
    "PositionalIndex",
    "Searcher",
    "DocResult",
    "Evidence",
]
