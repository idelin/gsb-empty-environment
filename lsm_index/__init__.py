"""LSM-style immutable-segment text index (standard library only)."""

from .index import LsmTextIndex, Snapshot
from .segment import DocRecord, Segment
from .tokenizer import tokenize

__all__ = ["LsmTextIndex", "Snapshot", "DocRecord", "Segment", "tokenize"]
