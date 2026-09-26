"""Stdlib-only ordered document-change indexing pipeline."""
from .buffer import SeqBuffer
from .events import OP_DELETE, OP_UPSERT, Event
from .index import InvertedIndex
from .pipeline import IndexingPipeline
from .tokenizer import term_counts, tokenize

__all__ = [
    "Event",
    "IndexingPipeline",
    "InvertedIndex",
    "OP_DELETE",
    "OP_UPSERT",
    "SeqBuffer",
    "term_counts",
    "tokenize",
]
