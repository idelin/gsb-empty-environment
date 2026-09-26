"""invidx：含词位置的倒排表与短语查询（纯标准库实现）。"""

from .index import Cursor, Index, Match, SearchResult, Snapshot
from .phrase import PhraseQuery, SlotHit
from .postings import BLOCK_SIZE, DecodeStats, FieldPostings
from .tokenize import Token, tokenize

__all__ = [
    "BLOCK_SIZE",
    "Cursor",
    "DecodeStats",
    "FieldPostings",
    "Index",
    "Match",
    "PhraseQuery",
    "SearchResult",
    "SlotHit",
    "Snapshot",
    "Token",
    "tokenize",
]

